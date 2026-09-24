package device

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

func testWAV() []byte {
	samples := 9600
	b := make([]byte, 44+samples*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(samples*2))
	for i := 0; i < samples; i++ {
		var v int16 = 2000
		if i%40 < 20 {
			v = -2000
		}
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(v))
	}
	return b
}
func TestDifyAudioChatAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wav := testWAV()
	packets, err := encodeOpus(ctx, wav)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing provider credential")
		}
		switch r.URL.Path {
		case "/audio-to-text":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("user") != "principal:test" {
				t.Error("wrong principal")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"text": "你好"})
		case "/chat-messages":
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v["query"] != "你好" || v["conversation_id"] != "previous" {
				t.Error(v)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"answer": "你好，测试成功。", "conversation_id": "next"})
		case "/text-to-audio":
			_, _ = w.Write(wav)
		default:
			http.NotFound(w, r)
		}
	}))
	defer h.Close()
	d := Dify{BaseURL: h.URL, APIKey: "test-key"}
	reply, err := d.Turn(ctx, "principal:test", "previous", packets)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || reply.Conversation != "next" || len(reply.Audio) == 0 {
		t.Fatal(reply, calls)
	}
}
