package brain

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// Text2VoiceBrain 实时语音合成（CosyVoice WebSocket）
//
// 时序：run-task -> 等 task-started -> (一个或多个) continue-task -> finish-task -> 接收 binary 音频分片 -> task-finished
//
// 输出：将服务端下发的二进制音频分片按顺序拼接到 BrainOutput.AudioData
type Text2VoiceBrain struct {
	config *Config
	dialer *websocket.Dialer
}

func NewText2VoiceBrain(config *QwenConfig) *Text2VoiceBrain {
	return &Text2VoiceBrain{
		config: config,
		dialer: websocket.DefaultDialer,
	}
}

type cosyRunTask struct {
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

type cosyContinueTask struct {
	Header struct {
		Action    string `json:"action"`
		TaskID    string `json:"task_id"`
		Streaming string `json:"streaming"`
	} `json:"header"`
	Payload struct {
		Input struct {
			Text string `json:"text"`
		} `json:"input"`
	} `json:"payload"`
}

type cosyFinishTask struct {
	Header struct {
		Action    string `json:"action"`
		TaskID    string `json:"task_id"`
		Streaming string `json:"streaming"`
	} `json:"header"`
	Payload struct {
		Input map[string]any `json:"input"`
	} `json:"payload"`
}

type cosyEvent struct {
	Header struct {
		TaskID       string         `json:"task_id"`
		Event        string         `json:"event"`
		ErrorCode    string         `json:"error_code,omitempty"`
		ErrorMessage string         `json:"error_message,omitempty"`
		Attributes   map[string]any `json:"attributes,omitempty"`
	} `json:"header"`
	Payload struct {
		Output map[string]any `json:"output,omitempty"`
		Usage  map[string]any `json:"usage,omitempty"`
	} `json:"payload,omitempty"`
}

func (b *Text2VoiceBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return &BrainOutput{Success: false, Error: "input is nil"}, fmt.Errorf("input is nil")
	}
	ctx := input.Context
	if ctx == nil {
		ctx = context.Background()
	}

	model := input.Model
	if model == "" {
		model = b.config.Model
	}
	if model == "" {
		model = "cosyvoice-v3-flash"
	}

	// 待合成文本：支持 input.Text 或 ExtraParams["texts"]（[]string）批量发送
	var texts []string
	if input.ExtraParams != nil {
		if v, ok := input.ExtraParams["texts"]; ok {
			if vs, ok := v.([]string); ok {
				for _, t := range vs {
					t = strings.TrimSpace(t)
					if t != "" {
						texts = append(texts, t)
					}
				}
			} else if vi, ok := v.([]any); ok {
				for _, it := range vi {
					if s, ok := it.(string); ok {
						s = strings.TrimSpace(s)
						if s != "" {
							texts = append(texts, s)
						}
					}
				}
			}
		}
	}
	if len(texts) == 0 {
		t := strings.TrimSpace(input.Text)
		if t == "" {
			return &BrainOutput{Success: false, Error: "missing text (BrainInput.Text)"}, fmt.Errorf("missing text (BrainInput.Text)")
		}
		texts = []string{t}
	}

	// run-task parameters 默认值（可被 ExtraParams 覆盖）
	params := map[string]any{
		"text_type":   "PlainText",
		"voice":       "longanyang",
		"format":      "mp3",
		"sample_rate": 22050,
		"volume":      50,
		"rate":        1.0,
		"pitch":       1.0,
		"enable_ssml": false,
	}
	if input.ExtraParams != nil {
		for k, v := range input.ExtraParams {
			// texts 是我们自己的批量输入字段，不属于 parameters
			if k == "texts" {
				continue
			}
			params[k] = v
		}
	}

	taskID, err := randomHex32()
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}

	conn, err := b.dial(ctx)
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}
	defer conn.Close()

	events := make(chan cosyEvent, 32)
	audioChunks := make(chan []byte, 64)
	readErr := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(audioChunks)
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			if msgType == websocket.BinaryMessage {
				// 复制一份，避免底层复用 buffer
				cp := make([]byte, len(msg))
				copy(cp, msg)
				audioChunks <- cp
				continue
			}
			if msgType != websocket.TextMessage {
				continue
			}
			var ev cosyEvent
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}
			events <- ev
		}
	}()

	// run-task
	run := cosyRunTask{}
	run.Header.Action = "run-task"
	run.Header.TaskID = taskID
	run.Header.Streaming = "duplex"
	run.Payload.TaskGroup = "audio"
	run.Payload.Task = "tts"
	run.Payload.Function = "SpeechSynthesizer"
	run.Payload.Model = model
	run.Payload.Parameters = params
	run.Payload.Input = map[string]any{}

	if err := conn.WriteJSON(run); err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}

	// wait task-started
	if err := b.waitEvent(ctx, events, readErr, "task-started"); err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}

	// send continue-task(s)
	for _, t := range texts {
		ct := cosyContinueTask{}
		ct.Header.Action = "continue-task"
		ct.Header.TaskID = taskID
		ct.Header.Streaming = "duplex"
		ct.Payload.Input.Text = t
		if err := conn.WriteJSON(ct); err != nil {
			return &BrainOutput{Success: false, Error: err.Error()}, err
		}
	}

	// finish-task：必须尽快发送，避免 23s 超时
	fin := cosyFinishTask{}
	fin.Header.Action = "finish-task"
	fin.Header.TaskID = taskID
	fin.Header.Streaming = "duplex"
	fin.Payload.Input = map[string]any{}
	if err := conn.WriteJSON(fin); err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}

	var audio bytes.Buffer
	meta := map[string]any{
		"task_id": taskID,
		"model":   model,
	}

	// consume until task-finished / task-failed
	for {
		select {
		case <-ctx.Done():
			return &BrainOutput{Success: false, Error: ctx.Err().Error(), Metadata: meta}, ctx.Err()
		case err := <-readErr:
			if err != nil {
				return &BrainOutput{Success: false, Error: err.Error(), Metadata: meta}, err
			}
		case chunk, ok := <-audioChunks:
			if ok && len(chunk) > 0 {
				audio.Write(chunk)
			}
		case ev, ok := <-events:
			if !ok {
				// 连接关闭：尽力返回已收到的音频
				out := audio.Bytes()
				cp := make([]byte, len(out))
				copy(cp, out)
				return &BrainOutput{Success: true, Mode: BrainModeVoiceGenerate, AudioData: cp, Metadata: meta}, nil
			}
			switch ev.Header.Event {
			case "result-generated":
				// usage.characters / request_uuid 等信息可在这里累积
				if ev.Payload.Usage != nil {
					meta["usage"] = ev.Payload.Usage
				}
			case "task-finished":
				if ev.Header.Attributes != nil {
					if reqUUID, ok := ev.Header.Attributes["request_uuid"]; ok {
						meta["request_uuid"] = reqUUID
					}
				}
				if ev.Payload.Usage != nil {
					meta["usage"] = ev.Payload.Usage
				}
				out := audio.Bytes()
				cp := make([]byte, len(out))
				copy(cp, out)
				return &BrainOutput{
					Success:   true,
					Mode:      BrainModeVoiceGenerate,
					AudioData: cp,
					Metadata:  meta,
				}, nil
			case "task-failed":
				msg := ev.Header.ErrorMessage
				if msg == "" {
					msg = "task failed"
				}
				if ev.Header.ErrorCode != "" {
					msg = fmt.Sprintf("%s: %s", ev.Header.ErrorCode, msg)
				}
				return &BrainOutput{Success: false, Error: msg, Metadata: meta}, fmt.Errorf("%s", msg)
			}
		}
	}
}

func (b *Text2VoiceBrain) waitEvent(ctx context.Context, events <-chan cosyEvent, readErr <-chan error, want string) error {
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
				return fmt.Errorf("%s", msg)
			}
		}
	}
}

func (b *Text2VoiceBrain) dial(ctx context.Context) (*websocket.Conn, error) {
	h := http.Header{}
	h.Set("Authorization", fmt.Sprintf("bearer %s", b.config.APIKey))
	h.Set("User-Agent", "PeopleAgent/1.0")
	// 如需开启数据合规检测，可在 ExtraParams 里自己加 header 映射；这里保持默认不启用
	conn, _, err := b.dialer.DialContext(ctx, b.getWSURL(), h)
	return conn, err
}

func (b *Text2VoiceBrain) getWSURL() string {
	if endpoint, ok := b.config.APIEndpoints["tts_ws"]; ok && strings.TrimSpace(endpoint) != "" {
		return endpoint
	}
	switch b.config.Region {
	case RegionSingapore:
		return "wss://dashscope-intl.aliyuncs.com/api-ws/v1/inference/"
	default:
		return "wss://dashscope.aliyuncs.com/api-ws/v1/inference/"
	}
}

func randomHex32() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
