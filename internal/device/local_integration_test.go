package device

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"golang.org/x/net/websocket"
	"os"
	"testing"
	"time"
)

// Opt-in test exercises real Opus conversion, local ASR, the published Chatflow,
// and the device WebSocket. It never takes over the physical device connection.
func TestLocalVoiceToText(t *testing.T) {
	if os.Getenv("SPIDER_TEST_LOCAL_VOICE") != "1" {
		t.Skip("set SPIDER_TEST_LOCAL_VOICE=1 for live ASR and Dify")
	}
	root := "../../data/device/"
	b, err := os.ReadFile(root + "dify-app.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		APIKey string `json:"api_key"`
	}
	if err = json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	wav, err := os.ReadFile(root + "test-input.wav")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	packets, err := encodeOpus(ctx, wav)
	if err != nil {
		t.Fatal(err)
	}
	d := &Dify{BaseURL: "http://localhost/v1", APIKey: cfg.APIKey, ASRURL: "http://127.0.0.1:8085/transcribe", TextOnly: true}
	s, h := setup(t)
	s.Provider = d
	ws := dial(t, h)
	_ = ws.SetDeadline(time.Now().Add(100 * time.Second))
	hash := enroll(t, ws)
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.voice.capture_state.v1", "capture_enabled": true, "held_by": []string{"observation_lease"}})
	for seq, packet := range packets {
		frame := make([]byte, 12+len(packet))
		copy(frame, "OBS1")
		binary.LittleEndian.PutUint32(frame[4:], hash)
		binary.LittleEndian.PutUint32(frame[8:], uint32(seq))
		copy(frame[12:], packet)
		if err := wire.Send(ws, wireMessage{true, frame}); err != nil {
			t.Fatal(err)
		}
	}
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.voice.capture_state.v1", "capture_enabled": false, "held_by": []string{"observation_lease"}})
	transcript, answer := recv(t, ws), recv(t, ws)
	if transcript["role"] != "user" || answer["role"] != "assistant" || answer["audio_expected"] != false {
		t.Fatal(transcript, answer)
	}
	t.Logf("transcript=%s answer=%s", transcript["content"], answer["content"])
	_ = websocket.JSON.Send(ws, map[string]any{"type": "node.device.ping.v1"})
	if recv(t, ws)["type"] != "node.device.pong.v1" {
		t.Fatal("unexpected audio or rejection")
	}
}
