package device

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/net/websocket"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Binding struct {
	Node      string `json:"node_id"`
	Principal string `json:"principal_id"`
	Token     string `json:"token"`
}
type Server struct {
	Bindings   []Binding
	AdminToken string
	Provider   Provider
	Store      *Store
	mu         sync.Mutex
	active     map[string]bool
}

func equalToken(a, b string) bool {
	return len(a) >= 24 && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"service":"spider-device","protocol":"mantle-device.v1"}`)
	})
	mux.HandleFunc("GET /v1/device/ws", func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var binding *Binding
		for i := range s.Bindings {
			if equalToken(s.Bindings[i].Token, token) {
				binding = &s.Bindings[i]
				break
			}
		}
		if binding == nil {
			http.Error(w, "unauthorized", 401)
			return
		}
		s.mu.Lock()
		if s.active == nil {
			s.active = map[string]bool{}
		}
		busy := s.active[binding.Node]
		if !busy {
			s.active[binding.Node] = true
		}
		s.mu.Unlock()
		if busy {
			http.Error(w, "device already connected", 409)
			return
		}
		defer func() { s.mu.Lock(); delete(s.active, binding.Node); s.mu.Unlock() }()
		ws := websocket.Server{Handshake: func(c *websocket.Config, r *http.Request) error {
			if r.Header.Get("Origin") != "" {
				return errors.New("browser origins are not supported")
			}
			return nil
		}, Handler: func(ws *websocket.Conn) { s.serve(ws, *binding) }}
		ws.ServeHTTP(w, r)
	})
	mux.HandleFunc("POST /v1/device/approvals", func(w http.ResponseWriter, r *http.Request) {
		if !equalToken(s.AdminToken, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var a Approval
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		d.DisallowUnknownFields()
		if d.Decode(&a) != nil {
			http.Error(w, "invalid approval", 400)
			return
		}
		bound := false
		for _, b := range s.Bindings {
			bound = bound || (b.Node == a.Node && b.Principal == a.Principal)
		}
		if !bound {
			http.Error(w, "unknown binding", 400)
			return
		}
		if err := s.Store.Create(a); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(201)
	})
	mux.HandleFunc("GET /v1/device/approvals/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !equalToken(s.AdminToken, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			http.Error(w, "unauthorized", 401)
			return
		}
		a, ok := s.Store.Get(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a)
	})
	return mux
}

type wireMessage struct {
	Binary bool
	Data   []byte
}

var wire = websocket.Codec{Marshal: func(v any) ([]byte, byte, error) {
	m := v.(wireMessage)
	if m.Binary {
		return m.Data, websocket.BinaryFrame, nil
	}
	return m.Data, websocket.TextFrame, nil
}, Unmarshal: func(data []byte, kind byte, v any) error {
	m := v.(*wireMessage)
	m.Binary = kind == websocket.BinaryFrame
	m.Data = append([]byte(nil), data...)
	return nil
}}

func randomID() string { var b [12]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func randomHash() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}
func strict(data []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func (s *Server) serve(ws *websocket.Conn, b Binding) {
	defer ws.Close()
	ws.MaxPayloadBytes = 32768
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var writeMu sync.Mutex
	send := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		// The WebSocket reader also writes automatic Pong control frames.
		// Do not leave an expired application-write deadline on that path.
		defer ws.SetWriteDeadline(time.Time{})
		return websocket.JSON.Send(ws, v)
	}
	reject := func(subject, reason string) {
		_ = send(map[string]any{"type": "node.rejected.v0", "subject": subject, "reason": reason})
	}
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var first wireMessage
	if wire.Receive(ws, &first) != nil || first.Binary {
		return
	}
	var enroll struct {
		Type         string   `json:"type"`
		Node         string   `json:"node_id"`
		Principal    string   `json:"principal_id"`
		Classes      []string `json:"supported_classes"`
		Interrupt    *bool    `json:"allows_interruption"`
		Capabilities []struct {
			Claim     string `json:"claim_id"`
			Canonical string `json:"canonical_capability"`
		} `json:"capabilities"`
	}
	if strict(first.Data, &enroll) != nil || enroll.Type != "node.enroll.v0" || enroll.Node != b.Node || enroll.Principal != b.Principal || enroll.Interrupt == nil {
		reject("enrollment", "identity is not authorized")
		return
	}
	classes := map[string]bool{}
	for _, c := range enroll.Classes {
		if classes[c] || (c != "compact" && c != "minimal" && c != "full") {
			reject("enrollment", "invalid surface classes")
			return
		}
		classes[c] = true
	}
	if !classes["compact"] {
		reject("enrollment", "compact surface support required")
		return
	}
	claims := map[string]bool{}
	for _, c := range enroll.Capabilities {
		if c.Canonical == "" || c.Claim != "node-capability:"+c.Canonical || claims[c.Claim] {
			reject("enrollment", "invalid capability")
			return
		}
		claims[c.Claim] = true
	}
	if send(map[string]any{"type": "node.enrolled.v0", "node_id": b.Node, "principal_id": b.Principal}) != nil {
		return
	}
	slog.Info("device enrolled", "node", b.Node)
	grant := randomHash()
	if send(map[string]any{"type": "node.voice.observation.v1", "state": "granted", "surface_hash": grant}) != nil {
		return
	}
	input := make(chan wireMessage, 8)
	readErr := make(chan error, 1)
	go func() {
		for {
			_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
			var m wireMessage
			if err := wire.Receive(ws, &m); err != nil {
				readErr <- err
				return
			}
			select {
			case input <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	type turnResult struct {
		reply      Reply
		err        error
		generation uint64
	}
	results := make(chan turnResult, 1)
	var packets [][]byte
	var seq uint32
	capture, busy := false, false
	conversation := ""
	var turnCancel context.CancelFunc
	activeApproval := ""
	pendingTurn := ""
	var pendingHash uint32
	var generation uint64
	type streamResult struct {
		generation uint64
		err        error
	}
	streamed := make(chan streamResult, 1)
	outputSent := false
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-readErr:
			slog.Info("device disconnected", "node", b.Node, "reason", err)
			return
		case <-ticker.C:
			pending := s.Store.Pending(b.Node, b.Principal)
			sort.Slice(pending, func(i, j int) bool { return pending[i].Expires.Before(pending[j].Expires) })
			if len(pending) > 0 && pending[0].ID != activeApproval {
				activeApproval = pending[0].ID
				if send(projection(pending[0])) != nil {
					return
				}
			}
		case m := <-input:
			if m.Binary {
				if !capture || busy {
					continue
				}
				if len(m.Data) <= 12 || len(m.Data) > 1512 || string(m.Data[:4]) != "OBS1" {
					reject("voice", "invalid observation frame")
					return
				}
				if binary.LittleEndian.Uint32(m.Data[4:]) != grant || binary.LittleEndian.Uint32(m.Data[8:]) != seq {
					reject("voice", "invalid observation attribution or sequence")
					return
				}
				seq++
				if len(packets) >= 334 {
					reject("voice", "utterance exceeds 20 seconds")
					capture = false
					packets = nil
					continue
				}
				packets = append(packets, m.Data[12:])
				continue
			}
			var tag struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(m.Data, &tag) != nil {
				return
			}
			switch tag.Type {
			case "node.device.ping.v1":
				var ping struct {
					Type string `json:"type"`
				}
				if strict(m.Data, &ping) != nil {
					return
				}
				if send(map[string]any{"type": "node.device.pong.v1"}) != nil {
					return
				}
			case "node.voice.capture_state.v1":
				var c struct {
					Type    string   `json:"type"`
					Enabled bool     `json:"capture_enabled"`
					Held    []string `json:"held_by"`
					Detail  string   `json:"detail,omitempty"`
				}
				if strict(m.Data, &c) != nil {
					reject("voice", "invalid capture control")
					continue
				}
				if c.Enabled {
					if busy || pendingTurn != "" {
						reject("voice", "previous response is still active")
						continue
					}
					if len(c.Held) != 1 || c.Held[0] != "observation_lease" {
						reject("voice", "capture requires observation lease")
						continue
					}
					capture = true
					packets = nil
				} else if capture {
					capture = false
					if len(packets) == 0 {
						continue
					}
					if s.Provider == nil {
						reject("voice", "voice provider is not configured")
						packets = nil
						continue
					}
					busy = true
					slog.Info("device capture complete", "node", b.Node, "frames", len(packets))
					audio := packets
					packets = nil
					turnCtx, stop := context.WithTimeout(ctx, 120*time.Second)
					turnCancel = stop
					current := conversation
					generation++
					thisGeneration := generation
					go func() {
						defer stop()
						reply, err := s.Provider.Turn(turnCtx, b.Principal, current, audio)
						select {
						case results <- turnResult{reply, err, thisGeneration}:
						case <-ctx.Done():
						}
					}()
				}
			case "node.voice.output_receipt.v1":
				var r struct {
					Type   string `json:"type"`
					Turn   string `json:"turn_ref"`
					Played int    `json:"played_through"`
				}
				if strict(m.Data, &r) != nil || r.Turn != pendingTurn || pendingTurn == "" || r.Played != 0 || !outputSent {
					reject("voice", "invalid playback receipt")
					continue
				}
				slog.Info("device playback receipt", "node", b.Node, "turn", pendingTurn)
				pendingTurn = ""
			case "node.voice.cancel.v1":
				// Spider additive half-duplex extension: explicit user cancellation.
				var r struct {
					Type string `json:"type"`
				}
				if strict(m.Data, &r) != nil {
					continue
				}
				if turnCancel != nil {
					turnCancel()
				}
				capture = false
				packets = nil
				pendingTurn = ""
				busy = false
				generation++
				grant = randomHash()
				seq = 0
				if send(map[string]any{"type": "node.voice.observation.v1", "state": "granted", "surface_hash": grant}) != nil {
					return
				}
			case "node.ruling.v0":
				var r struct {
					Type      string `json:"type"`
					Principal string `json:"principal_id"`
					Decision  string `json:"decision_id"`
					Revision  int    `json:"decision_revision"`
					Action    string `json:"action"`
					Option    string `json:"option_id"`
				}
				if strict(m.Data, &r) != nil || r.Principal != b.Principal || r.Action != "choose" || r.Decision != activeApproval {
					reject("ruling", "selection is not from the live projection")
					continue
				}
				if err := s.Store.Choose(r.Decision, b.Node, b.Principal, r.Option, r.Revision); err != nil {
					reject("ruling", err.Error())
					continue
				}
				_ = send(map[string]any{"type": "node.ruling-accepted.v0", "decision_id": r.Decision, "decision_revision": r.Revision})
				activeApproval = ""
			default:
				reject("message", "unsupported device frame")
			}
		case completed := <-streamed:
			if completed.generation != generation {
				continue
			}
			if completed.err != nil {
				return
			}
			outputSent = true
			if send(map[string]any{"type": "node.voice.speech.v1", "state": "end", "turn_ref": pendingTurn, "segment_index": 0}) != nil {
				return
			}
		case result := <-results:
			if result.generation != generation {
				continue
			}
			busy = false
			if result.err != nil {
				reject("voice", "voice processing failed; check Spider provider configuration")
				continue
			}
			conversation = result.reply.Conversation
			turn := "turn:" + randomID()
			pendingTurn = turn
			pendingHash = randomHash()
			for _, line := range []struct{ role, text string }{{"user", result.reply.Transcript}, {"assistant", result.reply.Text}} {
				if send(map[string]any{"type": "node.converse.reply.v0", "message_id": randomID(), "situation_id": nil, "content": line.text, "role": line.role, "delivery": "live"}) != nil {
					return
				}
			}
			if len(result.reply.Audio) == 0 {
				pendingTurn = ""
				reject("voice", "empty speech response")
				continue
			}
			if send(map[string]any{"type": "node.voice.speech.v1", "state": "start", "turn_ref": turn, "turn_hash": pendingHash, "sample_rate": 16000, "segment_index": 0}) != nil {
				return
			}

			outputSent = false
			playCtx, stop := context.WithCancel(ctx)
			turnCancel = stop
			thisGeneration, hash := generation, pendingHash
			audio := result.reply.Audio
			go func() {
				err := func() error {
					for i, p := range audio {
						if len(p) > 1500 || len(p) == 0 {
							return errors.New("invalid Opus payload")
						}
						select {
						case <-playCtx.Done():
							return playCtx.Err()
						default:
						}
						data := make([]byte, len(p)+12)
						copy(data, "AVT1")
						binary.LittleEndian.PutUint32(data[4:], hash)
						binary.LittleEndian.PutUint32(data[8:], uint32(i))
						copy(data[12:], p)
						writeMu.Lock()
						_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
						err := wire.Send(ws, wireMessage{true, data})
						_ = ws.SetWriteDeadline(time.Time{})
						writeMu.Unlock()
						if err != nil {
							return err
						}
						timer := time.NewTimer(60 * time.Millisecond)
						select {
						case <-timer.C:
						case <-playCtx.Done():
							timer.Stop()
							return playCtx.Err()
						}
					}
					return nil
				}()
				stop()
				select {
				case streamed <- streamResult{thisGeneration, err}:
				case <-ctx.Done():
				}
			}()
		}
	}
}
