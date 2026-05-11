package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/utils/logging"
)

const maxVoiceASRStreamTurn = 20 << 20 // 20 MiB per utterance binary from client

type funASRRunTaskWire struct {
	Header struct {
		Action    string `json:"action"`
		TaskID    string `json:"task_id"`
		Streaming string `json:"streaming"`
	} `json:"header"`
	Payload struct {
		TaskGroup  string         `json:"task_group"`
		Task       string         `json:"task"`
		Function   string         `json:"function"`
		Model      string         `json:"model"`
		Parameters map[string]any `json:"parameters"`
		Input      map[string]any `json:"input"`
	} `json:"payload"`
}

type funASRFinishTaskWire struct {
	Header struct {
		Action    string `json:"action"`
		TaskID    string `json:"task_id"`
		Streaming string `json:"streaming"`
	} `json:"header"`
	Payload struct {
		Input map[string]any `json:"input"`
	} `json:"payload"`
}

type funASREventWire struct {
	Header struct {
		TaskID       string `json:"task_id"`
		Event        string `json:"event"`
		ErrorCode    string `json:"error_code,omitempty"`
		ErrorMessage string `json:"error_message,omitempty"`
	} `json:"header"`
	Payload struct {
		Output struct {
			Sentence struct {
				Text        string `json:"text"`
				SentenceEnd bool   `json:"sentence_end"`
			} `json:"sentence"`
		} `json:"output"`
	} `json:"payload"`
}

type asrClientMsg struct {
	Op         string `json:"op"`
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
}

type asrStreamSession struct {
	mu      sync.RWMutex
	up      *websocket.Conn
	active  bool
	taskID  string
	recvCtr int

	pumpCtx    context.Context
	pumpCancel context.CancelFunc
	pumpWG     sync.WaitGroup
}

func randomASRBridgeTaskID32() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func writeASRClientJSON(mu *sync.Mutex, conn *websocket.Conn, v any) error {
	mu.Lock()
	defer mu.Unlock()
	return conn.WriteJSON(v)
}

func waitASRUpstreamEvent(ctx context.Context, up *websocket.Conn, want string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, raw, err := up.ReadMessage()
		if err != nil {
			return err
		}
		var ev funASREventWire
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		switch ev.Header.Event {
		case want:
			return nil
		case "task-failed":
			msg := ev.Header.ErrorMessage
			if msg == "" {
				msg = "task failed"
			}
			if ev.Header.ErrorCode != "" {
				msg = fmt.Sprintf("%s: %s", ev.Header.ErrorCode, msg)
			}
			return fmt.Errorf("%s", msg)
		default:
			continue
		}
	}
}

func pumpASRUpstreamIntoChan(ctx context.Context, up *websocket.Conn, remote string, sink chan<- funASREventWire) {
	defer close(sink)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_, raw, err := up.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				logging.Warn("[voice.asr.stream] upstream_read_err client_remote=%s err=%v", remote, err)
			}
			return
		}
		var ev funASREventWire
		if err := json.Unmarshal(raw, &ev); err != nil {
			logging.Warn("[voice.asr.stream] upstream_skip_non_json bytes=%d err=%v", len(raw), err)
			continue
		}
		select {
		case sink <- ev:
		case <-ctx.Done():
			return
		}
	}
}

func finalizeASRUttPump(ctx context.Context, remote string, taskID string, clientMu *sync.Mutex,
	client *websocket.Conn, events <-chan funASREventWire,
) {
	var transcript strings.Builder
	var lastText string

	for ev := range events {
		select {
		case <-ctx.Done():
			return
		default:
		}
		switch ev.Header.Event {
		case "result-generated":
			txt := strings.TrimSpace(ev.Payload.Output.Sentence.Text)
			if txt == "" {
				continue
			}
			lastText = txt
			_ = writeASRClientJSON(clientMu, client, map[string]any{
				"op":           "partial",
				"text":         txt,
				"sentence_end": ev.Payload.Output.Sentence.SentenceEnd,
				"task_id":      taskID,
			})
			if ev.Payload.Output.Sentence.SentenceEnd {
				if transcript.Len() > 0 {
					transcript.WriteByte('\n')
				}
				transcript.WriteString(txt)
			}
		case "task-finished":
			final := strings.TrimSpace(transcript.String())
			if final == "" {
				final = strings.TrimSpace(lastText)
			}
			logging.Info("[voice.asr.stream] phase=upstream_task_finished client_remote=%s task_id=%s transcript_runes=%d",
				remote, taskID, len([]rune(final)))
			_ = writeASRClientJSON(clientMu, client, map[string]any{"op": "final", "text": final, "task_id": taskID})
			return
		case "task-failed":
			msg := ev.Header.ErrorMessage
			if msg == "" {
				msg = "task failed"
			}
			if ev.Header.ErrorCode != "" {
				msg = fmt.Sprintf("%s: %s", ev.Header.ErrorCode, msg)
			}
			logging.Error("[voice.asr.stream] upstream_task_failed client_remote=%s task_id=%s err=%s", remote, taskID, msg)
			_ = writeASRClientJSON(clientMu, client, map[string]any{"op": "error", "message": msg, "task_id": taskID})
			return
		default:
			continue
		}
	}
	logging.Warn("[voice.asr.stream] upstream_eof_before_final client_remote=%s task_id=%s", remote, taskID)
	_ = writeASRClientJSON(clientMu, client, map[string]any{"op": "error", "message": "upstream closed before task finished", "task_id": taskID})
	return
}

func (s *asrStreamSession) closeUpstream() {
	if s.pumpCancel != nil {
		s.pumpCancel()
	}
	s.pumpWG.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pumpCancel = nil
	s.pumpCtx = nil
	if s.up != nil {
		_ = s.up.Close()
		s.up = nil
	}
	s.active = false
	s.taskID = ""
	s.recvCtr = 0
}

func (s *asrStreamSession) getUpstreamLocked() (*websocket.Conn, bool) {
	return s.up, s.active
}

func handleVoiceASRStream(w http.ResponseWriter, r *http.Request, asrBrain *brain.Speech2TxtASRBrain) {
	start := time.Now()
	remote := r.RemoteAddr
	if asrBrain == nil {
		logging.Error("[voice.asr.stream] phase=reject reason=asr_brain_nil client_remote=%s", remote)
		http.Error(w, "asr unavailable", http.StatusServiceUnavailable)
		return
	}

	conn, err := ttsStreamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Warn("[voice.asr.stream] ws_upgrade_failed client_remote=%s err=%v", remote, err)
		return
	}
	logging.Info("[voice.asr.stream] phase=ws_upgrade_ok client_remote=%s query_masked=%s", remote, dialogMaskRawQuery(r.URL.RawQuery))

	var clientMu sync.Mutex
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer conn.Close()

	_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "welcome", "path": "/v1/voice/asr/stream"})
	defer func() {
		logging.Info("[voice.asr.stream] phase=handler_done client_remote=%s elapsed=%s", remote, time.Since(start).Truncate(time.Millisecond))
	}()

	st := &asrStreamSession{}
	defer st.closeUpstream()

	for {
		if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
			return
		}
		mt, data, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				logging.Warn("[voice.asr.stream] client_read_err client_remote=%s err=%v", remote, err)
			}
			return
		}

		if mt == websocket.BinaryMessage {
			st.mu.RLock()
			up, ok := st.getUpstreamLocked()
			rec := st.recvCtr
			st.mu.RUnlock()
			if !ok || up == nil {
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": "send binary after {\"op\":\"start\"}; no active upstream"})
				continue
			}
			if rec+len(data) > maxVoiceASRStreamTurn {
				st.closeUpstream()
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": fmt.Sprintf("audio exceeds %d bytes per utterance", maxVoiceASRStreamTurn)})
				continue
			}
			st.mu.Lock()
			st.recvCtr += len(data)
			st.mu.Unlock()
			if err := up.WriteMessage(websocket.BinaryMessage, data); err != nil {
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": "upstream write: " + err.Error()})
				st.closeUpstream()
			}
			continue
		}

		var msg asrClientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": fmt.Sprintf("invalid json: %v", err)})
			continue
		}
		op := strings.ToLower(strings.TrimSpace(msg.Op))

		switch op {
		case "ping":
			_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "pong", "t": time.Now().UnixMilli()})

		case "start":
			st.closeUpstream()
			sampleRate := msg.SampleRate
			if sampleRate <= 0 {
				sampleRate = 16000
			}
			format := strings.ToLower(strings.TrimSpace(msg.Format))
			if format == "" {
				format = "pcm"
			}
			if format != "pcm" && format != "wav" {
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": "start.format must be pcm or wav"})
				continue
			}

			up, err := asrBrain.DialUpstream(ctx)
			if err != nil {
				logging.Warn("[voice.asr.stream] upstream_dial_failed client_remote=%s err=%v", remote, err)
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": "upstream dial: " + err.Error()})
				continue
			}
			taskID, err := randomASRBridgeTaskID32()
			if err != nil {
				_ = up.Close()
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": err.Error()})
				continue
			}
			model := asrBrain.ResolvedModel()

			run := funASRRunTaskWire{}
			run.Header.Action = "run-task"
			run.Header.TaskID = taskID
			run.Header.Streaming = "duplex"
			run.Payload.TaskGroup = "audio"
			run.Payload.Task = "asr"
			run.Payload.Function = "recognition"
			run.Payload.Model = model
			run.Payload.Parameters = map[string]any{
				"format":      format,
				"sample_rate": sampleRate,
			}
			run.Payload.Input = map[string]any{}

			if err := up.WriteJSON(run); err != nil {
				_ = up.Close()
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": "run-task write: " + err.Error()})
				continue
			}
			if err := waitASRUpstreamEvent(ctx, up, "task-started"); err != nil {
				_ = up.Close()
				logging.Warn("[voice.asr.stream] task_start_failed client_remote=%s err=%v", remote, err)
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": err.Error()})
				continue
			}

			pCtx, pumpCancel := context.WithCancel(ctx)
			events := make(chan funASREventWire, 32)
			st.pumpCtx = pCtx
			st.pumpCancel = pumpCancel
			st.pumpWG.Add(2)
			st.mu.Lock()
			st.up = up
			st.active = true
			st.taskID = taskID
			st.recvCtr = 0
			st.mu.Unlock()

			go func() {
				defer st.pumpWG.Done()
				pumpASRUpstreamIntoChan(pCtx, up, remote, events)
			}()
			go func(upConn *websocket.Conn) {
				defer st.pumpWG.Done()
				defer pumpCancel()
				finalizeASRUttPump(pCtx, remote, taskID, &clientMu, conn, events)
				st.mu.Lock()
				st.active = false
				if st.up == upConn {
					if st.up != nil {
						_ = st.up.Close()
						st.up = nil
					}
				}
				st.recvCtr = 0
				st.taskID = ""
				st.pumpCtx = nil
				st.pumpCancel = nil
				st.mu.Unlock()
			}(up)

			logging.Info("[voice.asr.stream] phase=start_ok client_remote=%s model=%q format=%s sr=%d task_id=%s",
				remote, model, format, sampleRate, taskID)
			_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "started", "format": format, "sample_rate": sampleRate, "task_id": taskID})

		case "finish":
			st.mu.RLock()
			up := st.up
			tid := st.taskID
			active := st.active
			recvBytes := st.recvCtr
			st.mu.RUnlock()
			if up == nil || !active || tid == "" {
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": "no active upstream; send start then audio before finish"})
				continue
			}
			logging.Info("[voice.asr.stream] phase=finish_send client_remote=%s task_id=%s recv_bytes=%d",
				remote, tid, recvBytes)

			fin := funASRFinishTaskWire{}
			fin.Header.Action = "finish-task"
			fin.Header.TaskID = tid
			fin.Header.Streaming = "duplex"
			fin.Payload.Input = map[string]any{}
			if err := up.WriteJSON(fin); err != nil {
				_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": "finish-task write: " + err.Error()})
				st.closeUpstream()
				continue
			}

		default:
			_ = writeASRClientJSON(&clientMu, conn, map[string]any{"op": "error", "message": fmt.Sprintf("unknown op %q", msg.Op)})
		}
	}
}