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

// handleVoiceTTSStream exposes a WebSocket that bridges client text to realtime TTS.
//
// Upgrade: GET /v1/voice/tts/stream
// Query (optional): voice, model, mode, language_type, format, sample_rate, instructions, optimize_instructions (true/false)
//
// 模型选择：若 serve 从 config/llm.yaml 读取的 brains.voice_realtime_ws.model 非空，则优先使用；否则使用 query 的 model；再否则由 SDK 按 brains.voice 与内置规则回退。
//
// Log chain (grep-friendly):
//   - [voice.tts.client] — this handler: client/browser ↔ fullmodel.
//   - [voice.realtime_ws] leg=upstream — SDK outbound WebSocket handshake to realtime provider (endpoint_key tts_realtime_ws).
//   - [voice.tts.upstream] — JSON/audio protocol on that upstream session after handshake.
//
// Client → server (text JSON): {"op":"append","text":"..."} | {"op":"commit"} | {"op":"finish"} | {"op":"clear"} | {"op":"ping"}
// Server → client: binary frames = PCM chunks; text JSON {"op":"event","type":"..."} (non-audio events),
//
//	{"op":"error","message":"..."}, {"op":"pong"}, {"op":"done"}
func handleVoiceTTSStream(w http.ResponseWriter, r *http.Request, sdk *fullmodel.Client, modelFromVoiceRealtimeWSConfig string) {
	start := time.Now()
	if sdk == nil {
		logging.Error("[voice.tts.client] phase=reject chain_step=0 reason=sdk_nil")
		http.Error(w, "voice client unavailable", http.StatusServiceUnavailable)
		return
	}

	conn, err := ttsStreamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Warn("[voice.tts.client] phase=ws_upgrade_failed chain_step=1 client_remote=%s err=%v flow=\"HTTP upgrade to fullmodel /v1/voice/tts/stream failed (client↔fullmodel only)\"", r.RemoteAddr, err)
		return
	}
	logging.Info("[voice.tts.client] phase=ws_upgrade_ok chain_step=1/3 client_remote=%s path=%s query=%s flow=\"client websocket to fullmodel is open; next step opens upstream realtime TTS\"",
		conn.RemoteAddr(), r.URL.Path, r.URL.RawQuery)
	defer func() {
		logging.Info("[voice.tts.client] phase=handler_done client_remote=%s elapsed=%s", conn.RemoteAddr(), time.Since(start).Truncate(time.Millisecond))
		logging.Info("[voice.tts.client] phase=client_conn_close client_remote=%s", conn.RemoteAddr())
		conn.Close()
	}()
	ww := &wsConnWriter{conn: conn}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := r.URL.Query()
	model := strings.TrimSpace(modelFromVoiceRealtimeWSConfig)
	if model == "" {
		model = strings.TrimSpace(q.Get("model"))
	}
	cfg := fullmodel.RealtimeTTSConfig{
		Voice:                firstNonEmpty(q.Get("voice"), "Cherry"),
		Model:                model,
		Mode:                 firstNonEmpty(q.Get("mode"), fullmodel.QwenRealtimeModeServerCommit),
		LanguageType:         firstNonEmpty(q.Get("language_type"), "Chinese"),
		ResponseFormat:       firstNonEmpty(q.Get("format"), "pcm"),
		SampleRate:           queryPositiveInt(q, "sample_rate", 24000),
		Instructions:         strings.TrimSpace(q.Get("instructions")),
		OptimizeInstructions: strings.EqualFold(q.Get("optimize_instructions"), "true") || q.Get("optimize_instructions") == "1",
	}
	logging.Info("[voice.tts.client] phase=tts_config chain_step=1 client_remote=%s voice=%q model_effective=%q model_query=%q mode=%s format=%s sample_rate=%d lang=%q instruct_len=%d optimize_instr=%v",
		conn.RemoteAddr(), cfg.Voice, cfg.Model, strings.TrimSpace(q.Get("model")), cfg.Mode, cfg.ResponseFormat, cfg.SampleRate, cfg.LanguageType, len(cfg.Instructions), cfg.OptimizeInstructions)

	logging.Info("[voice.tts.client] chain_step=2/3 phase=before_RealtimeTTS client_remote=%s flow=\"fullmodel calls sdk.RealtimeTTS → outbound WS dial [voice.realtime_ws] auth=brains.voice.api_key url=brains.voice.endpoints.tts_realtime_ws or default region URL\"",
		conn.RemoteAddr())
	session, err := sdk.RealtimeTTS(ctx, cfg)
	if err != nil {
		logging.Error("[voice.tts.client] chain_step=2/3 phase=FAILED client_remote=%s elapsed=%s err=%v diagnose=\"upstream not ready: look for [voice.realtime_ws] ws_handshake_failed or [voice.tts.upstream] errors at same time; client_ws is still OK\"",
			conn.RemoteAddr(), time.Since(start).Truncate(time.Millisecond), err)
		_ = ww.writeJSON(map[string]any{"op": "error", "message": err.Error()})
		return
	}
	logging.Info("[voice.tts.client] chain_step=2/3 phase=upstream_ready client_remote=%s elapsed=%s flow=\"upstream WS up+session.update sent; pumping audio/events to client\"",
		conn.RemoteAddr(), time.Since(start).Truncate(time.Millisecond))
	defer func() {
		logging.Info("[voice.tts.client] phase=upstream_session_close client_remote=%s", conn.RemoteAddr())
		session.Close()
	}()

	pumpDone := make(chan struct{})
	logging.Info("[voice.tts.client] chain_step=3/3 phase=pump_start client_remote=%s flow=\"forward upstream PCM/events → client websocket\"",
		conn.RemoteAddr())
	go pumpRealtimeTTS(ctx, session, ww, pumpDone, conn.RemoteAddr().String(), 0)

	readErr := readTTSClientOps(ctx, session, cfg.Mode, ww)
	if readErr != nil {
		logging.Warn("[voice.tts.client] phase=read_ops_done client_remote=%s err=%v elapsed=%s", conn.RemoteAddr(), readErr, time.Since(start).Truncate(time.Millisecond))
		cancel()
	} else {
		logging.Info("[voice.tts.client] phase=read_ops_ok client_remote=%s finish_op_ok elapsed=%s", conn.RemoteAddr(), time.Since(start).Truncate(time.Millisecond))
	}
	<-pumpDone
	logging.Info("[voice.tts.client] phase=pump_joined client_remote=%s elapsed=%s", conn.RemoteAddr(), time.Since(start).Truncate(time.Millisecond))

	if readErr != nil && !websocket.IsCloseError(readErr, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		logging.Warn("[voice.tts.client] phase=write_client_error client_remote=%s err=%v", conn.RemoteAddr(), readErr)
		_ = ww.writeJSON(map[string]any{"op": "error", "message": readErr.Error()})
	}
}

// pumpRealtimeTTS forwards TTS output in event order (audio deltas as binary, other events as JSON).
// dialogTurnSeq > 0 时使用 [voice.dialog] 前缀并把 turn_seq 写入字段，便于在 default.log 里一条 grep 串联语音对话链路；纯 TTS 入口传 0。
func pumpRealtimeTTS(ctx context.Context, session *fullmodel.RealtimeTTSSession, ww *wsConnWriter, done chan<- struct{}, remote string, dialogTurnSeq int) {
	tag := "[voice.tts.client]"
	turnFld := ""
	if dialogTurnSeq > 0 {
		tag = "[voice.dialog]"
		turnFld = fmt.Sprintf(" turn_seq=%d", dialogTurnSeq)
	}
	var audioChunks int
	var audioBytes int64
	defer func() {
		logging.Info("%s chain_step=tts_pump_exit client_remote=%s%s audio_chunks=%d audio_bytes=%d pcm_to_client_done=true ctx_done=%v",
			tag, remote, turnFld, audioChunks, audioBytes, ctx.Err() != nil)
		close(done)
	}()
	logging.Info("%s phase=tts_pcm_pump_begin client_remote=%s%s flow=\"upstream realtime TTS → binary PCM/events → websocket client\"", tag, remote, turnFld)
	for {
		select {
		case <-ctx.Done():
			logging.Info("%s phase=tts_pcm_pump_ctx_done client_remote=%s%s err=%v", tag, remote, turnFld, ctx.Err())
			return
		case ev, ok := <-session.Events():
			if !ok {
				logging.Info("%s phase=tts_events_chan_closed client_remote=%s%s flow=\"sent op:done to client if session ended\"", tag, remote, turnFld)
				_ = ww.writeJSON(map[string]any{"op": "done"})
				return
			}
			if ev.Error != "" {
				rawDetail := ""
				if ev.Raw != nil {
					if b, err := json.Marshal(ev.Raw); err == nil {
						rawDetail = previewRunes(string(b), 8000)
					}
				}
				logging.Error("%s phase=tts_upstream_err client_remote=%s%s code=%s msg=%s upstream_json=%s diagnose=\"see [voice.tts.upstream] upstream_error\"", tag, remote, turnFld, ev.ErrorCode, ev.Error, rawDetail)
				_ = ww.writeJSON(map[string]any{"op": "error", "message": ev.Error, "error_code": ev.ErrorCode})
				return
			}
			if ev.Type == "response.audio.delta" {
				if len(ev.Audio) > 0 {
					audioChunks++
					audioBytes += int64(len(ev.Audio))
					if audioChunks == 1 {
						logging.Info("%s phase=first_pcm_to_client client_remote=%s%s pcm_chunk_bytes=%d flow=\"first audio delta forwarded after AppendText\"", tag, remote, turnFld, len(ev.Audio))
					}
					if audioChunks == 1 || audioChunks%100 == 0 {
						logging.Info("%s phase=tts_pcm_progress client_remote=%s%s pcm_chunks=%d pcm_bytes_total=%d last_pcm_bytes=%d",
							tag, remote, turnFld, audioChunks, audioBytes, len(ev.Audio))
					}
					if audioChunks == 1 || audioChunks%20 == 0 {
						logging.Info("%s phase=tts_pcm_write_begin client_remote=%s%s pcm_chunks=%d pcm_chunk_bytes=%d pcm_bytes_total=%d",
							tag, remote, turnFld, audioChunks, len(ev.Audio), audioBytes)
					}
					if err := ww.writeBinary(ev.Audio); err != nil {
						logging.Warn("%s phase=tts_pcm_write_failed client_remote=%s%s err=%v", tag, remote, turnFld, err)
						return
					}
					if audioChunks == 1 || audioChunks%20 == 0 {
						logging.Info("%s phase=tts_pcm_write_ok client_remote=%s%s pcm_chunks=%d pcm_bytes_total=%d",
							tag, remote, turnFld, audioChunks, audioBytes)
					}
				} else {
					logging.Warn("%s phase=tts_pcm_empty_delta client_remote=%s%s", tag, remote, turnFld)
				}
				continue
			}
			payload := map[string]any{"op": "event", "type": ev.Type}
			logging.Info("%s phase=tts_upstream_event client_remote=%s%s event_type=%s", tag, remote, turnFld, ev.Type)
			if err := ww.writeJSON(payload); err != nil {
				logging.Warn("%s phase=tts_event_write_json_failed client_remote=%s%s err=%v", tag, remote, turnFld, err)
				return
			}
			if ev.Type == "session.finished" {
				logging.Info("%s phase=tts_session_finished_event client_remote=%s%s flow=\"provider closed session\"", tag, remote, turnFld)
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
			logging.Warn("[voice.tts.client] client_invalid_json client_remote=%s bytes=%d err=%v", conn.RemoteAddr(), len(data), err)
			_ = ww.writeJSON(map[string]any{"op": "error", "message": fmt.Sprintf("invalid json: %v", err)})
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Op)) {
		case "ping":
			logging.Info("[voice.tts.client] client_op ping client_remote=%s", conn.RemoteAddr())
			if err := ww.writeJSON(map[string]any{"op": "pong", "t": time.Now().UnixMilli()}); err != nil {
				return err
			}
		case "append":
			logging.Info("[voice.tts.client] client_op append client_remote=%s text_len=%d preview=%s",
				conn.RemoteAddr(), len([]rune(msg.Text)), previewRunes(msg.Text, 48))
			if err := session.AppendText(msg.Text); err != nil {
				return err
			}
		case "commit":
			logging.Info("[voice.tts.client] client_op commit client_remote=%s", conn.RemoteAddr())
			if mode != fullmodel.QwenRealtimeModeCommit {
				return fmt.Errorf("commit only valid when mode=%s", fullmodel.QwenRealtimeModeCommit)
			}
			if err := session.Commit(); err != nil {
				return err
			}
		case "clear":
			logging.Info("[voice.tts.client] client_op clear client_remote=%s", conn.RemoteAddr())
			if err := session.Clear(); err != nil {
				return err
			}
		case "finish":
			logging.Info("[voice.tts.client] client_op finish client_remote=%s", conn.RemoteAddr())
			if err := session.Finish(); err != nil {
				return err
			}
			return nil
		default:
			logging.Warn("[voice.tts.client] client_op unknown client_remote=%s op=%q", conn.RemoteAddr(), msg.Op)
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
