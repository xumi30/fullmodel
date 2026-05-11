package brain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xumi30/fullmodel/utils/logging"
)

// Speech2TxtASRBrain 实时语音识别（Fun-ASR WebSocket）
//
// 流程：run-task -> 等 task-started -> 发送音频二进制流 -> finish-task -> 等 task-finished
type Speech2TxtASRBrain struct {
	config *Config
	dialer *websocket.Dialer
}

func NewSpeech2TxtASRBrain(config *QwenConfig) *Speech2TxtASRBrain {
	return &Speech2TxtASRBrain{
		config: config,
		dialer: websocket.DefaultDialer,
	}
}

type funASRRunTask struct {
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

type funASRFinishTask struct {
	Header struct {
		Action    string `json:"action"`
		TaskID    string `json:"task_id"`
		Streaming string `json:"streaming"`
	} `json:"header"`
	Payload struct {
		Input map[string]any `json:"input"`
	} `json:"payload"`
}

type funASREvent struct {
	Header struct {
		TaskID       string         `json:"task_id"`
		Event        string         `json:"event"`
		ErrorCode    string         `json:"error_code,omitempty"`
		ErrorMessage string         `json:"error_message,omitempty"`
		Attributes   map[string]any `json:"attributes,omitempty"`
	} `json:"header"`
	Payload struct {
		Output struct {
			Sentence struct {
				Text        string `json:"text"`
				SentenceEnd bool   `json:"sentence_end"`
				Heartbeat   *bool  `json:"heartbeat,omitempty"`
			} `json:"sentence"`
		} `json:"output"`
		Usage map[string]any `json:"usage,omitempty"`
	} `json:"payload"`
}

func (b *Speech2TxtASRBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return brainError("input is nil"), fmt.Errorf("input is nil")
	}
	ctx := input.ContextOrBackground()

	audio := input.Media.Audio.Data
	if len(audio) == 0 {
		return brainError("missing audio bytes (BrainInput.Media.Audio.Data)"), fmt.Errorf("missing audio bytes (BrainInput.Media.Audio.Data)")
	}

	model := input.Options.Model
	if model == "" {
		model = b.config.Model
	}
	if model == "" {
		model = "fun-asr-realtime"
	}

	params := map[string]any{
		"sample_rate": 16000,
		"format":      "wav",
	}
	if input.Extra != nil {
		// 允许用户覆盖 sample_rate / format / 其他 Fun-ASR 参数
		for k, v := range input.Extra {
			params[k] = v
		}
	}

	chunkSize := 1024
	if v, ok := params["chunk_size"].(int); ok && v > 0 {
		chunkSize = v
	}
	interval := 100 * time.Millisecond
	if v, ok := params["chunk_interval_ms"]; ok {
		switch t := v.(type) {
		case int:
			if t > 0 {
				interval = time.Duration(t) * time.Millisecond
			}
		case float64:
			if t > 0 {
				interval = time.Duration(int(t)) * time.Millisecond
			}
		}
	}

	taskID, err := randomTaskID32()
	if err != nil {
		return brainError(err.Error()), err
	}

	conn, err := b.dial(ctx)
	if err != nil {
		return brainError(err.Error()), err
	}
	defer conn.Close()

	// 接收事件协程
	events := make(chan funASREvent, 16)
	readErr := make(chan error, 1)
	go func() {
		defer close(events)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			var ev funASREvent
			if err := json.Unmarshal(msg, &ev); err != nil {
				logging.Warn("[brain.asr] skip_invalid_json_event bytes=%d err=%v preview=%s",
					len(msg), err, logging.TruncateRunes(string(msg), 240))
				continue
			}
			events <- ev
		}
	}()

	// 发送 run-task
	run := funASRRunTask{}
	run.Header.Action = "run-task"
	run.Header.TaskID = taskID
	run.Header.Streaming = "duplex"
	run.Payload.TaskGroup = "audio"
	run.Payload.Task = "asr"
	run.Payload.Function = "recognition"
	run.Payload.Model = model
	run.Payload.Parameters = map[string]any{
		"format":      params["format"],
		"sample_rate": params["sample_rate"],
	}
	// 透传其他可选 parameters（过滤掉我们自己的 chunk 控制参数）
	for k, v := range params {
		if k == "format" || k == "sample_rate" || k == "chunk_size" || k == "chunk_interval_ms" {
			continue
		}
		run.Payload.Parameters[k] = v
	}
	run.Payload.Input = map[string]any{}

	if err := conn.WriteJSON(run); err != nil {
		return brainError(err.Error()), err
	}

	// 等 task-started
	if err := b.waitEvent(ctx, events, readErr, "task-started"); err != nil {
		return brainError(err.Error()), err
	}

	// 发送音频二进制流
	for off := 0; off < len(audio); off += chunkSize {
		end := off + chunkSize
		if end > len(audio) {
			end = len(audio)
		}

		select {
		case <-ctx.Done():
			return brainError(ctx.Err().Error()), ctx.Err()
		default:
		}

		if err := conn.WriteMessage(websocket.BinaryMessage, audio[off:end]); err != nil {
			return brainError(err.Error()), err
		}
		time.Sleep(interval)
	}

	// 发送 finish-task
	fin := funASRFinishTask{}
	fin.Header.Action = "finish-task"
	fin.Header.TaskID = taskID
	fin.Header.Streaming = "duplex"
	fin.Payload.Input = map[string]any{}
	if err := conn.WriteJSON(fin); err != nil {
		return brainError(err.Error()), err
	}

	// 收集 result-generated，直到 task-finished 或 task-failed
	var transcript strings.Builder
	var lastText string

	for {
		select {
		case <-ctx.Done():
			return brainError(ctx.Err().Error()), ctx.Err()
		case err := <-readErr:
			if err != nil {
				return brainError(err.Error()), err
			}
		case ev, ok := <-events:
			if !ok {
				// 连接结束
				final := strings.TrimSpace(transcript.String())
				if final == "" {
					final = strings.TrimSpace(lastText)
				}
				result := brainSuccess(BrainModeASR)
				result.Content.Text = final
				return &result, nil
			}

			switch ev.Header.Event {
			case "result-generated":
				txt := strings.TrimSpace(ev.Payload.Output.Sentence.Text)
				if txt != "" {
					lastText = txt
					if ev.Payload.Output.Sentence.SentenceEnd {
						// 句子结束：追加一行
						if transcript.Len() > 0 {
							transcript.WriteString("\n")
						}
						transcript.WriteString(txt)
					}
				}
			case "task-finished":
				final := strings.TrimSpace(transcript.String())
				if final == "" {
					final = strings.TrimSpace(lastText)
				}
				result := brainSuccess(BrainModeASR)
				result.Content.Text = final
				result.Metadata = map[string]any{"task_id": taskID}
				return &result, nil
			case "task-failed":
				msg := ev.Header.ErrorMessage
				if msg == "" {
					msg = "task failed"
				}
				if ev.Header.ErrorCode != "" {
					msg = fmt.Sprintf("%s: %s", ev.Header.ErrorCode, msg)
				}
				logging.Error("[brain.asr] task_failed detail=%s", logging.CompactJSONForLog(ev, 12000))
				return brainErrorWithMetadata(msg, map[string]any{"task_id": taskID}), fmt.Errorf("%s", msg)
			}
		}
	}
}

func (b *Speech2TxtASRBrain) waitEvent(ctx context.Context, events <-chan funASREvent, readErr <-chan error, want string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			if err != nil {
				return err
			}
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("connection closed before %s", want)
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
				logging.Error("[brain.asr] wait_event task_failed want=%s detail=%s", want, logging.CompactJSONForLog(ev, 12000))
				return fmt.Errorf("%s", msg)
			}
		}
	}
}

func (b *Speech2TxtASRBrain) dial(ctx context.Context) (*websocket.Conn, error) {
	h := http.Header{}
	// 文档示例使用 bearer（小写）也可，服务端一般大小写不敏感
	h.Set("Authorization", fmt.Sprintf("bearer %s", b.config.APIKey))
	h.Set("User-Agent", "PeopleAgent/1.0")
	conn, resp, err := b.dialer.DialContext(ctx, b.getWSURL(), h)
	if err != nil && resp != nil && resp.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))
		_ = resp.Body.Close()
		logging.Error("[brain.asr] dial_failed err=%v detail=%s", err,
			logging.CompactJSONForLog(map[string]any{"http_status": resp.Status, "body": string(body)}, 12000))
	}
	return conn, err
}

// DialUpstream 打开 DashScope Fun-ASR 双工推理 WebSocket（与本 Brain ProcessInput 内共用 dial 逻辑）。
func (b *Speech2TxtASRBrain) DialUpstream(ctx context.Context) (*websocket.Conn, error) {
	return b.dial(ctx)
}

// ResolvedASRModel 返回配置的 ASR model；未配置时使用 fun-asr-realtime。
func (b *Speech2TxtASRBrain) ResolvedModel() string {
	model := ""
	if b != nil && b.config != nil {
		model = strings.TrimSpace(b.config.Model)
	}
	if model == "" {
		return "fun-asr-realtime"
	}
	return model
}

func (b *Speech2TxtASRBrain) getWSURL() string {
	if endpoint, ok := b.config.APIEndpoints["asr_ws"]; ok && strings.TrimSpace(endpoint) != "" {
		return endpoint
	}
	switch b.config.Region {
	case RegionSingapore:
		return "wss://dashscope-intl.aliyuncs.com/api-ws/v1/inference/"
	default:
		return "wss://dashscope.aliyuncs.com/api-ws/v1/inference/"
	}
}

func randomTaskID32() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil // 32 hex chars
}
