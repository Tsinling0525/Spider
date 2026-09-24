package device

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"golang.org/x/net/websocket"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct{}

func (fakeProvider) Turn(ctx context.Context, user, conv string, p [][]byte) (Reply, error) {
	return Reply{Transcript: "test input", Text: "test reply", Conversation: "c1", Audio: [][]byte{{0xf8, 0xff, 0xfe}}}, nil
}
func setup(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "approvals.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Bindings: []Binding{{"node:esp32", "principal:owner", strings.Repeat("a", 32)}}, AdminToken: strings.Repeat("b", 32), Provider: fakeProvider{}, Store: store}
	h := httptest.NewServer(s.Handler())
	t.Cleanup(h.Close)
	return s, h
}
func dial(t *testing.T, h *httptest.Server) *websocket.Conn {
	t.Helper()
	c, _ := websocket.NewConfig("ws"+strings.TrimPrefix(h.URL, "http")+"/v1/device/ws", "http://localhost")
	c.Origin = &url.URL{}
	c.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	ws, err := websocket.DialConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	_ = ws.SetDeadline(time.Now().Add(8 * time.Second))
	return ws
}
func recv(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	var value map[string]any
	if err := websocket.JSON.Receive(ws, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func enroll(t *testing.T, ws *websocket.Conn) uint32 {
	t.Helper()
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.enroll.v0", "node_id": "node:esp32", "principal_id": "principal:owner", "supported_classes": []string{"compact"}, "allows_interruption": true})
	if recv(t, ws)["type"] != "node.enrolled.v0" {
		t.Fatal("not enrolled")
	}
	g := recv(t, ws)
	return uint32(g["surface_hash"].(float64))
}
func TestVoiceRoundAndReceipt(t *testing.T) {
	_, h := setup(t)
	ws := dial(t, h)
	hash := enroll(t, ws)
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.voice.capture_state.v1", "capture_enabled": true, "held_by": []string{"observation_lease"}})
	packet := []byte{'O', 'B', 'S', '1', 0, 0, 0, 0, 0, 0, 0, 0, 0xf8, 0xff, 0xfe}
	binary.LittleEndian.PutUint32(packet[4:], hash)
	if err := wire.Send(ws, wireMessage{true, packet}); err != nil {
		t.Fatal(err)
	}
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.voice.capture_state.v1", "capture_enabled": false, "held_by": []string{"observation_lease"}})
	if recv(t, ws)["role"] != "user" || recv(t, ws)["role"] != "assistant" {
		t.Fatal("missing transcript")
	}
	start := recv(t, ws)
	if start["state"] != "start" {
		t.Fatal(start)
	}
	var audio wireMessage
	if err := wire.Receive(ws, &audio); err != nil {
		t.Fatal(err)
	}
	if !audio.Binary || string(audio.Data[:4]) != "AVT1" || binary.LittleEndian.Uint32(audio.Data[4:]) != uint32(start["turn_hash"].(float64)) {
		t.Fatal("bad audio attribution")
	}
	if recv(t, ws)["state"] != "end" {
		t.Fatal("missing end")
	}
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.voice.output_receipt.v1", "turn_ref": start["turn_ref"], "played_through": 0})
}

func TestIdlePingAfterWriteDeadline(t *testing.T) {
	_, h := setup(t)
	ws := dial(t, h)
	enroll(t, ws)
	// Reproduce the physical board's idle heartbeat after the server's last
	// five-second write deadline has elapsed. A control Pong must still work.
	time.Sleep(5100 * time.Millisecond)
	ping := websocket.Codec{Marshal: func(any) ([]byte, byte, error) {
		return []byte("idle"), websocket.PingFrame, nil
	}}
	if err := ping.Send(ws, nil); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Send(ws, map[string]any{"type": "node.device.ping.v1"}); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, ws); got["type"] != "node.device.pong.v1" {
		t.Fatal(got)
	}
}
func TestUnauthorizedAndForeignEnrollment(t *testing.T) {
	_, h := setup(t)
	r, err := http.Get(h.URL + "/v1/device/ws")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	ws := dial(t, h)
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.enroll.v0", "node_id": "node:other", "principal_id": "principal:owner", "supported_classes": []string{"compact"}, "allows_interruption": true})
	if recv(t, ws)["subject"] != "enrollment" {
		t.Fatal("foreign identity accepted")
	}
}
func TestInvalidAudioAttribution(t *testing.T) {
	_, h := setup(t)
	ws := dial(t, h)
	hash := enroll(t, ws)
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.voice.capture_state.v1", "capture_enabled": true, "held_by": []string{"observation_lease"}})
	packet := make([]byte, 13)
	copy(packet, "OBS1")
	binary.LittleEndian.PutUint32(packet[4:], hash+1)
	_ = wire.Send(ws, wireMessage{true, packet})
	if recv(t, ws)["subject"] != "voice" {
		t.Fatal("foreign grant accepted")
	}
}
func TestApprovalSelectionIsDurableAndBounded(t *testing.T) {
	s, h := setup(t)
	a := Approval{ID: "d1", Node: "node:esp32", Principal: "principal:owner", Text: "Confirm test", Options: []Option{{"yes", "Approve"}, {"no", "Deny"}}, Expires: time.Now().Add(time.Minute)}
	if err := s.Store.Create(a); err != nil {
		t.Fatal(err)
	}
	ws := dial(t, h)
	enroll(t, ws)
	p := recv(t, ws)
	if p["schema_version"] != "surface.channel-record.v0" {
		t.Fatal(p)
	}
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.ruling.v0", "principal_id": "principal:owner", "decision_id": "d1", "decision_revision": 1, "action": "choose", "option_id": "unoffered"})
	if recv(t, ws)["type"] != "node.rejected.v0" {
		t.Fatal("accepted unoffered option")
	}
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.ruling.v0", "principal_id": "principal:owner", "decision_id": "d1", "decision_revision": 1, "action": "choose", "option_id": "no"})
	if recv(t, ws)["type"] != "node.ruling-accepted.v0" {
		t.Fatal("missing receipt")
	}
	reopened, err := NewStore(s.Store.path)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := reopened.Get("d1")
	if stored.Choice != "no" {
		t.Fatal("decision not persisted")
	}
	if reopened.Choose("d1", a.Node, a.Principal, "yes", 1) == nil {
		t.Fatal("duplicate ruling accepted")
	}
}
func TestOggRoundTrip(t *testing.T) {
	input := [][]byte{{0xf8, 0xff, 0xfe}, make([]byte, 255)}
	output, err := oggPackets(opusOgg(input))
	if err != nil || len(output) != 2 || len(output[1]) != 255 {
		t.Fatal(output, err)
	}
	if _, err = oggPackets([]byte("OggS")); err == nil {
		t.Fatal("short data accepted")
	}
}
func TestExpiredApproval(t *testing.T) {
	s, _ := setup(t)
	err := s.Store.Create(Approval{ID: "expired", Node: "n", Principal: "p", Text: "x", Options: []Option{{"yes", "yes"}}, Expires: time.Now().Add(-time.Second)})
	if err == nil {
		t.Fatal("expired approval accepted")
	}
}
func TestProjectionJSON(t *testing.T) {
	a := Approval{ID: "d", Node: "node:esp32", Principal: "principal:owner", Text: "test", Options: []Option{{"yes", "yes"}}, Revision: 1, Expires: time.Now().Add(time.Minute)}
	if _, err := json.Marshal(projection(a)); err != nil {
		t.Fatal(err)
	}
}

func TestCancelRotatesGrant(t *testing.T) {
	_, h := setup(t)
	ws := dial(t, h)
	old := enroll(t, ws)
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.voice.cancel.v1"})
	grant := recv(t, ws)
	if grant["type"] != "node.voice.observation.v1" || uint32(grant["surface_hash"].(float64)) == old {
		t.Fatal("grant not rotated")
	}
}
