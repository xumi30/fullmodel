package brain

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Image2VideoGenerateBrain 图生视频（HappyHorse I2V，异步接口：创建任务 -> 轮询获取）
//
// 文档：
// 1) POST /api/v1/services/aigc/video-generation/video-synthesis  (Header: X-DashScope-Async: enable)
// 2) GET  /api/v1/tasks/{task_id}
//
// 输入：首帧图片（必选）+ 可选 prompt，引导生成视频。
type Image2VideoGenerateBrain struct {
	config *Config
	client *http.Client
}

func NewImage2VideoGenerateBrain(config *QwenConfig) *Image2VideoGenerateBrain {
	return &Image2VideoGenerateBrain{
		config: config,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type happyHorseI2VCreateTaskRequest struct {
	Model      string                `json:"model"`
	Input      happyHorseI2VInput     `json:"input"`
	Parameters map[string]any         `json:"parameters,omitempty"`
}

type happyHorseI2VInput struct {
	Prompt string               `json:"prompt,omitempty"`
	Media  []happyHorseI2VMedia `json:"media"`
}

type happyHorseI2VMedia struct {
	Type string `json:"type"` // first_frame
	URL  string `json:"url"`
}

type happyHorseI2VCreateTaskResponse struct {
	Output struct {
		TaskStatus string `json:"task_status"`
		TaskID     string `json:"task_id"`
	} `json:"output,omitempty"`

	RequestID string `json:"request_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

type happyHorseTaskResponse struct {
	RequestID string `json:"request_id,omitempty"`
	Output    struct {
		TaskID        string `json:"task_id"`
		TaskStatus    string `json:"task_status"`
		SubmitTime    string `json:"submit_time,omitempty"`
		ScheduledTime string `json:"scheduled_time,omitempty"`
		EndTime       string `json:"end_time,omitempty"`
		OrigPrompt    string `json:"orig_prompt,omitempty"`
		VideoURL      string `json:"video_url,omitempty"`
		Code          string `json:"code,omitempty"`
		Message       string `json:"message,omitempty"`
	} `json:"output,omitempty"`

	Usage   map[string]any `json:"usage,omitempty"`
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message,omitempty"`
}

func (b *Image2VideoGenerateBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return &BrainOutput{Success: false, Error: "input is nil"}, fmt.Errorf("input is nil")
	}

	ctx := input.Context
	if ctx == nil {
		ctx = context.Background()
	}

	firstFrameURL, err := b.extractFirstFrameURL(input)
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}

	model := input.Model
	if model == "" {
		model = b.config.Model
	}
	if model == "" {
		model = "happyhorse-1.0-i2v"
	}

	parameters := map[string]any(nil)
	if input.ExtraParams != nil {
		if p, ok := input.ExtraParams["parameters"].(map[string]any); ok {
			parameters = p
		} else {
			parameters = input.ExtraParams
		}
	}

	pollInterval := 15 * time.Second
	if parameters != nil {
		if v, ok := parameters["poll_interval_sec"]; ok {
			switch t := v.(type) {
			case int:
				if t > 0 {
					pollInterval = time.Duration(t) * time.Second
				}
			case int64:
				if t > 0 {
					pollInterval = time.Duration(t) * time.Second
				}
			case float64:
				if t > 0 {
					pollInterval = time.Duration(int(t)) * time.Second
				}
			case json.Number:
				if n, err := t.Int64(); err == nil && n > 0 {
					pollInterval = time.Duration(n) * time.Second
				}
			}
		}
	}

	prompt := strings.TrimSpace(input.Text) // 可选

	taskID, requestID, err := b.createTask(ctx, model, prompt, firstFrameURL, parameters)
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error(), Metadata: map[string]any{"request_id": requestID}}, err
	}

	videoURL, meta, err := b.pollTask(ctx, taskID, pollInterval)
	if err != nil {
		if meta == nil {
			meta = map[string]any{}
		}
		meta["task_id"] = taskID
		meta["create_request_id"] = requestID
		return &BrainOutput{Success: false, Error: err.Error(), Metadata: meta}, err
	}

	meta["task_id"] = taskID
	meta["create_request_id"] = requestID
	meta["model"] = model

	return &BrainOutput{
		Success:  true,
		Mode:     BrainImage2VideoGenerate,
		VideoURL: videoURL,
		Metadata: meta,
	}, nil
}

func (b *Image2VideoGenerateBrain) extractFirstFrameURL(input *BrainInput) (string, error) {
	if strings.TrimSpace(input.ImageURL) != "" {
		return input.ImageURL, nil
	}
	if len(input.ImageData) > 0 {
		return buildDataURL("", input.ImageData), nil
	}

	for _, p := range input.MultimodalParts {
		switch p.Type {
		case "image_url":
			if p.ImageURL != nil && strings.TrimSpace(p.ImageURL.URL) != "" {
				return p.ImageURL.URL, nil
			}
		case "image_data":
			if p.ImageData != nil && len(p.ImageData.Data) > 0 {
				return buildDataURL(p.ImageData.MimeType, p.ImageData.Data), nil
			}
		}
	}

	return "", fmt.Errorf("missing first frame image (need BrainInput.ImageURL/ImageData or multimodal_parts image_url/image_data)")
}

func (b *Image2VideoGenerateBrain) createTask(ctx context.Context, model, prompt, firstFrameURL string, parameters map[string]any) (taskID string, requestID string, err error) {
	reqBody := happyHorseI2VCreateTaskRequest{
		Model: model,
		Input: happyHorseI2VInput{
			Prompt: prompt,
			Media: []happyHorseI2VMedia{
				{Type: "first_frame", URL: firstFrameURL},
			},
		},
		Parameters: parameters,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", b.getCreateTaskURL(), bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", b.config.APIKey))
	httpReq.Header.Set("X-DashScope-Async", "enable")
	httpReq.Header.Set("User-Agent", "PeopleAgent/1.0")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("create task failed with status %d: %s", resp.StatusCode, string(body))
	}

	var decoded happyHorseI2VCreateTaskResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", "", err
	}
	if decoded.Code != "" {
		msg := decoded.Message
		if msg == "" {
			msg = decoded.Code
		}
		return "", decoded.RequestID, fmt.Errorf("create task failed: %s", msg)
	}
	if decoded.Output.TaskID == "" {
		return "", decoded.RequestID, fmt.Errorf("create task failed: missing task_id")
	}
	return decoded.Output.TaskID, decoded.RequestID, nil
}

func (b *Image2VideoGenerateBrain) pollTask(ctx context.Context, taskID string, pollInterval time.Duration) (videoURL string, meta map[string]any, err error) {
	meta = map[string]any{}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		u, status, reqID, usage, code, msg, err := b.fetchTask(ctx, taskID)
		if reqID != "" {
			meta["request_id"] = reqID
		}
		if usage != nil {
			meta["usage"] = usage
		}
		if err != nil {
			return "", meta, err
		}

		switch status {
		case "SUCCEEDED":
			if strings.TrimSpace(u) == "" {
				return "", meta, fmt.Errorf("task succeeded but video_url is empty")
			}
			return u, meta, nil
		case "FAILED":
			if code == "" {
				code = "FAILED"
			}
			if msg == "" {
				msg = "task failed"
			}
			return "", meta, fmt.Errorf("%s: %s", code, msg)
		case "CANCELED":
			return "", meta, fmt.Errorf("task canceled")
		case "UNKNOWN":
			return "", meta, fmt.Errorf("task unknown or expired (task_id valid for 24h)")
		case "PENDING", "RUNNING", "":
			// keep polling
		default:
			meta["task_status"] = status
		}

		select {
		case <-ctx.Done():
			return "", meta, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *Image2VideoGenerateBrain) fetchTask(ctx context.Context, taskID string) (videoURL string, status string, requestID string, usage map[string]any, code string, message string, err error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", b.getFetchTaskURL(taskID), nil)
	if err != nil {
		return "", "", "", nil, "", "", err
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", b.config.APIKey))
	httpReq.Header.Set("User-Agent", "PeopleAgent/1.0")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return "", "", "", nil, "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", "", nil, "", "", fmt.Errorf("fetch task failed with status %d: %s", resp.StatusCode, string(body))
	}

	var decoded happyHorseTaskResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", "", "", nil, "", "", err
	}

	if decoded.Code != "" {
		msg := decoded.Message
		if msg == "" {
			msg = decoded.Code
		}
		return "", decoded.Output.TaskStatus, decoded.RequestID, decoded.Usage, decoded.Code, msg, nil
	}

	return decoded.Output.VideoURL, decoded.Output.TaskStatus, decoded.RequestID, decoded.Usage, decoded.Output.Code, decoded.Output.Message, nil
}

func (b *Image2VideoGenerateBrain) getCreateTaskURL() string {
	if b.config.BaseURL != "" {
		return b.config.BaseURL
	}
	if endpoint, ok := b.config.APIEndpoints["video_synthesis"]; ok {
		return endpoint
	}
	switch b.config.Region {
	case RegionSingapore:
		return "https://dashscope-intl.aliyuncs.com/api/v1/services/aigc/video-generation/video-synthesis"
	default:
		return "https://dashscope.aliyuncs.com/api/v1/services/aigc/video-generation/video-synthesis"
	}
}

func (b *Image2VideoGenerateBrain) getFetchTaskURL(taskID string) string {
	if endpoint, ok := b.config.APIEndpoints["tasks"]; ok && strings.TrimSpace(endpoint) != "" {
		return strings.TrimRight(endpoint, "/") + "/" + taskID
	}
	base := "https://dashscope.aliyuncs.com/api/v1/tasks/"
	if b.config.Region == RegionSingapore {
		base = "https://dashscope-intl.aliyuncs.com/api/v1/tasks/"
	}
	return base + taskID
}

func buildDataURL(mime string, data []byte) string {
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mime, b64)
}