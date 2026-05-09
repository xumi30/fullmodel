package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xumi30/fullmodel"
	"github.com/xumi30/fullmodel/utils/logging"
)

var ttsStreamUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 32 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// wsConnWriter serializes WebSocket writes (gorilla/websocket is not concurrent-safe).
type wsConnWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsConnWriter) writeJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(v)
}

func (w *wsConnWriter) writeBinary(p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, p)
}

// handleVoiceTTSStream exposes a WebSocket that bridges to DashScope Qwen Realtime TTS.
//
// Upgrade: GET /v1/voice/tts/stream
// Query (optional): voice, model, mode, language_type, format, sample_rate, instructions, optimize_instructions (true/false)
//
// Client → server (text JSON): {"op":"append","text":"..."} | {"op":"commit"} | {"op":"finish"} | {"op":"clear"} | {"op":"ping"}
// Server → client: binary frames = PCM chunks; text JSON {"op":"event","type":"..."} (non-audio events),
//
//	{"op":"error","message":"..."}, {"op":"pong"}, {"op":"done"}
func handleVoiceTTSStream(w http.ResponseWriter, r *http.Request, sdk *fullmodel.Client) {
	if sdk == nil {
		logging.Error("[voice.ws] reject sdk_nil")
		http.Error(w, "voice client unavailable", http.StatusServiceUnavailable)
		return
	}

	conn, err := ttsStreamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Warn("[voice.ws] upgrade_failed remote=%s err=%v", r.RemoteAddr, err)
		return
	}
	logging.Info("[voice.ws] connected remote=%s path=%s query=%s", conn.RemoteAddr(), r.URL.Path, r.URL.RawQuery)
	defer func() {
		logging.Info("[voice.ws] client_conn_closed remote=%s", conn.RemoteAddr())
		conn.Close()
	}()
	ww := &wsConnWriter{conn: conn}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := r.URL.Query()
	cfg := fullmodel.RealtimeTTSConfig{
		Voice:                firstNonEmpty(q.Get("voice"), "Cherry"),
		Model:                strings.TrimSpace(q.Get("model")),
		Mode:                 firstNonEmpty(q.Get("mode"), fullmodel.QwenRealtimeModeServerCommit),
		LanguageType:         firstNonEmpty(q.Get("language_type"), "Chinese"),
		ResponseFormat:       firstNonEmpty(q.Get("format"), "pcm"),
		SampleRate:           queryPositiveInt(q, "sample_rate", 24000),
		Instructions:         strings.TrimSpace(q.Get("instructions")),
		OptimizeInstructions: strings.EqualFold(q.Get("optimize_instructions"), "true") || q.Get("optimize_instructions") == "1",
	}
	logging.Info("[voice.ws] tts_config voice=%q model_query=%q mode=%s format=%s sample_rate=%d lang=%q instruct_len=%d",
		cfg.Voice, cfg.Model, cfg.Mode, cfg.ResponseFormat, cfg.SampleRate, cfg.LanguageType, len(cfg.Instructions))

	session, err := sdk.RealtimeTTS(ctx, cfg)
	if err != nil {
		logging.Error("[voice.ws] RealtimeTTS_failed remote=%s err=%v", conn.RemoteAddr(), err)
		_ = ww.writeJSON(map[string]any{"op": "error", "message": err.Error()})
		return
	}
	defer session.Close()

	pumpDone := make(chan struct{})
	go pumpRealtimeTTS(ctx, session, ww, pumpDone, conn.RemoteAddr().String())

	readErr := readTTSClientOps(ctx, session, cfg.Mode, ww)
	if readErr != nil {
		logging.Warn("[voice.ws] read_ops_done remote=%s err=%v", conn.RemoteAddr(), readErr)
		cancel()
	} else {
		logging.Info("[voice.ws] read_ops_finished remote=%s (finish_op_ok)", conn.RemoteAddr())
	}
	<-pumpDone

	if readErr != nil && !websocket.IsCloseError(readErr, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		logging.Warn("[voice.ws] write_client_error remote=%s err=%v", conn.RemoteAddr(), readErr)
		_ = ww.writeJSON(map[string]any{"op": "error", "message": readErr.Error()})
	}
}

// pumpRealtimeTTS forwards TTS output in event order (audio deltas as binary, other events as JSON).
func pumpRealtimeTTS(ctx context.Context, session *fullmodel.RealtimeTTSSession, ww *wsConnWriter, done chan<- struct{}, remote string) {
	var audioChunks int
	var audioBytes int64
	defer func() {
		logging.Info("[voice.ws] pump_exit remote=%s audio_chunks=%d audio_bytes=%d ctx_done=%v",
			remote, audioChunks, audioBytes, ctx.Err() != nil)
		close(done)
	}()
	for {
		select {
		case <-ctx.Done():
			logging.Info("[voice.ws] pump_ctx_done remote=%s err=%v", remote, ctx.Err())
			return
		case ev, ok := <-session.Events():
			if !ok {
				logging.Info("[voice.ws] pump_events_closed remote=%s", remote)
				_ = ww.writeJSON(map[string]any{"op": "done"})
				return
			}
			if ev.Error != "" {
				logging.Error("[voice.ws] pump_upstream_err remote=%s code=%s msg=%s", remote, ev.ErrorCode, ev.Error)
				_ = ww.writeJSON(map[string]any{"op": "error", "message": ev.Error, "error_code": ev.ErrorCode})
				return
			}
			if ev.Type == "response.audio.delta" {
				if len(ev.Audio) > 0 {
					audioChunks++
					audioBytes += int64(len(ev.Audio))
					if audioChunks == 1 || audioChunks%100 == 0 {
						logging.Info("[voice.ws] pump_audio_progress remote=%s chunks=%d bytes_total=%d last_chunk=%d",
							remote, audioChunks, audioBytes, len(ev.Audio))
					}
					if err := ww.writeBinary(ev.Audio); err != nil {
						logging.Warn("[voice.ws] pump_write_binary_failed remote=%s err=%v", remote, err)
						return
					}
				} else {
					logging.Warn("[voice.ws] pump_empty_delta remote=%s", remote)
				}
				continue
			}
			payload := map[string]any{"op": "event", "type": ev.Type}
			logging.Info("[voice.ws] pump_event remote=%s type=%s", remote, ev.Type)
			if err := ww.writeJSON(payload); err != nil {
				logging.Warn("[voice.ws] pump_write_json_failed remote=%s err=%v", remote, err)
				return
			}
			if ev.Type == "session.finished" {
				logging.Info("[voice.ws] pump_session_finished remote=%s", remote)
				_ = ww.writeJSON(map[string]any{"op": "done"})
				return
			}
		}
	}
}

type ttsClientMsg struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

func readTTSClientOps(ctx context.Context, session *fullmodel.RealtimeTTSSession, mode string, ww *wsConnWriter) error {
	conn := ww.conn
	for {
		if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
			return err
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg ttsClientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			logging.Warn("[voice.ws] client_invalid_json remote=%s bytes=%d err=%v", conn.RemoteAddr(), len(data), err)
			_ = ww.writeJSON(map[string]any{"op": "error", "message": fmt.Sprintf("invalid json: %v", err)})
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Op)) {
		case "ping":
			logging.Info("[voice.ws] client_op ping remote=%s", conn.RemoteAddr())
			if err := ww.writeJSON(map[string]any{"op": "pong", "t": time.Now().UnixMilli()}); err != nil {
				return err
			}
		case "append":
			logging.Info("[voice.ws] client_op append remote=%s text_len=%d preview=%s",
				conn.RemoteAddr(), len([]rune(msg.Text)), previewRunes(msg.Text, 48))
			if err := session.AppendText(msg.Text); err != nil {
				return err
			}
		case "commit":
			logging.Info("[voice.ws] client_op commit remote=%s", conn.RemoteAddr())
			if mode != fullmodel.QwenRealtimeModeCommit {
				return fmt.Errorf("commit only valid when mode=%s", fullmodel.QwenRealtimeModeCommit)
			}
			if err := session.Commit(); err != nil {
				return err
			}
		case "clear":
			logging.Info("[voice.ws] client_op clear remote=%s", conn.RemoteAddr())
			if err := session.Clear(); err != nil {
				return err
			}
		case "finish":
			logging.Info("[voice.ws] client_op finish remote=%s", conn.RemoteAddr())
			if err := session.Finish(); err != nil {
				return err
			}
			return nil
		default:
			logging.Warn("[voice.ws] client_op unknown remote=%s op=%q", conn.RemoteAddr(), msg.Op)
			_ = ww.writeJSON(map[string]any{"op": "error", "message": fmt.Sprintf("unknown op %q", msg.Op)})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func queryPositiveInt(q url.Values, key string, fallback int) int {
	s := strings.TrimSpace(q.Get(key))
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func previewRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}
