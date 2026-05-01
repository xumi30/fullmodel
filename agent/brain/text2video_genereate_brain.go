package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VideoTextGenerateBrain 文生视频（HappyHorse，异步接口：创建任务 -> 轮询获取）
//
// 文档：
// 1) POST /api/v1/services/aigc/video-generation/video-synthesis  (Header: X-DashScope-Async: enable)
// 2) GET  /api/v1/tasks/{task_id}
//
// 说明：视频生成耗时较长（1-5分钟），建议通过 Context 控制超时。
type VideoTextGenerateBrain struct {
	config *Config
	client *http.Client
}

func NewVideoTextGenerateBrain(config *QwenConfig) *VideoTextGenerateBrain {
	return &VideoTextGenerateBrain{
		config: config,
		client: &http.Client{
			Timeout: 60 * time.Second, // 单次HTTP请求超时；整体超时由 ctx 控制
		},
	}
}

type happyHorseCreateTaskRequest struct {
	Model      string               `json:"model"`
	Input      happyHorseCreateInput `json:"input"`
	Parameters map[string]any        `json:"parameters,omitempty"`
}

type happyHorseCreateInput struct {
	Prompt string `json:"prompt"`
}

type happyHorseCreateTaskResponse struct {
	Output struct {
		TaskStatus string `json:"task_status"`
		TaskID     string `json:"task_id"`
	} `json:"output,omitempty"`

	RequestID string `json:"request_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

type dashscopeTaskResponse struct {
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

func (vb *VideoTextGenerateBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return &BrainOutput{Success: false, Error: "input is nil"}, fmt.Errorf("input is nil")
	}

	ctx := input.Context
	if ctx == nil {
		ctx = context.Background()
	}

	prompt := strings.TrimSpace(input.Text)
	if prompt == "" {
		return &BrainOutput{Success: false, Error: "missing prompt (BrainInput.Text)"}, fmt.Errorf("missing prompt (BrainInput.Text)")
	}

	model := input.Model
	if model == "" {
		model = vb.config.Model
	}
	if model == "" {
		model = "happyhorse-1.0-t2v"
	}

	parameters := map[string]any(nil)
	if input.ExtraParams != nil {
		// 允许用户把 parameters 作为 ExtraParams["parameters"] 传入
		if p, ok := input.ExtraParams["parameters"].(map[string]any); ok {
			parameters = p
		} else {
			parameters = input.ExtraParams
		}
	}

	// 轮询配置（默认 15 秒）
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

	// Step 1: create task
	taskID, requestID, err := vb.createTask(ctx, model, prompt, parameters)
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error(), Metadata: map[string]any{"request_id": requestID}}, err
	}

	// Step 2: poll task
	videoURL, meta, err := vb.pollTask(ctx, taskID, pollInterval)
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
		Mode:     BrainText2VideoGenerate,
		VideoURL: videoURL,
		Metadata: meta,
	}, nil
}

func (vb *VideoTextGenerateBrain) createTask(ctx context.Context, model, prompt string, parameters map[string]any) (taskID string, requestID string, err error) {
	reqBody := happyHorseCreateTaskRequest{
		Model: model,
		Input: happyHorseCreateInput{
			Prompt: prompt,
		},
		Parameters: parameters,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", vb.getCreateTaskURL(), bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", vb.config.APIKey))
	httpReq.Header.Set("X-DashScope-Async", "enable")
	httpReq.Header.Set("User-Agent", "PeopleAgent/1.0")

	resp, err := vb.client.Do(httpReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("create task failed with status %d: %s", resp.StatusCode, string(body))
	}

	var decoded happyHorseCreateTaskResponse
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

func (vb *VideoTextGenerateBrain) pollTask(ctx context.Context, taskID string, pollInterval time.Duration) (videoURL string, meta map[string]any, err error) {
	meta = map[string]any{}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		u, status, reqID, usage, code, msg, err := vb.fetchTask(ctx, taskID)
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

func (vb *VideoTextGenerateBrain) fetchTask(ctx context.Context, taskID string) (videoURL string, status string, requestID string, usage map[string]any, code string, message string, err error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", vb.getFetchTaskURL(taskID), nil)
	if err != nil {
		return "", "", "", nil, "", "", err
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", vb.config.APIKey))
	httpReq.Header.Set("User-Agent", "PeopleAgent/1.0")

	resp, err := vb.client.Do(httpReq)
	if err != nil {
		return "", "", "", nil, "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", "", nil, "", "", fmt.Errorf("fetch task failed with status %d: %s", resp.StatusCode, string(body))
	}

	var decoded dashscopeTaskResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", "", "", nil, "", "", err
	}

	// 顶层错误
	if decoded.Code != "" {
		msg := decoded.Message
		if msg == "" {
			msg = decoded.Code
		}
		return "", decoded.Output.TaskStatus, decoded.RequestID, decoded.Usage, decoded.Code, msg, nil
	}

	return decoded.Output.VideoURL, decoded.Output.TaskStatus, decoded.RequestID, decoded.Usage, decoded.Output.Code, decoded.Output.Message, nil
}

func (vb *VideoTextGenerateBrain) getCreateTaskURL() string {
	if vb.config.BaseURL != "" {
		return vb.config.BaseURL
	}
	if endpoint, ok := vb.config.APIEndpoints["video_synthesis"]; ok {
		return endpoint
	}
	switch vb.config.Region {
	case RegionSingapore:
		return "https://dashscope-intl.aliyuncs.com/api/v1/services/aigc/video-generation/video-synthesis"
	default:
		return "https://dashscope.aliyuncs.com/api/v1/services/aigc/video-generation/video-synthesis"
	}
}

func (vb *VideoTextGenerateBrain) getFetchTaskURL(taskID string) string {
	if endpoint, ok := vb.config.APIEndpoints["tasks"]; ok && strings.TrimSpace(endpoint) != "" {
		return strings.TrimRight(endpoint, "/") + "/" + taskID
	}
	base := "https://dashscope.aliyuncs.com/api/v1/tasks/"
	if vb.config.Region == RegionSingapore {
		base = "https://dashscope-intl.aliyuncs.com/api/v1/tasks/"
	}
	return base + taskID
}