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

// ImageGenerateBrain 文生图（Qwen-Image，同步接口）
//
// 文档：POST /api/v1/services/aigc/multimodal-generation/generation
// 请求体：
// {
//   "model": "qwen-image-2.0-pro",
//   "input": {"messages":[{"role":"user","content":[{"text":"...prompt..."}]}]},
//   "parameters": {...}
// }
//
// 响应体：output.choices[0].message.content[0].image 为临时URL（24小时有效）
type ImageGenerateBrain struct {
	config *Config
	client *http.Client
}

func NewImageGenerateBrain(config *QwenConfig) *ImageGenerateBrain {
	return &ImageGenerateBrain{
		config: config,
		client: &http.Client{
			Timeout: 180 * time.Second,
		},
	}
}

type qwenImageGenerateRequest struct {
	Model      string                 `json:"model"`
	Input      qwenImageGenerateInput  `json:"input"`
	Parameters map[string]any          `json:"parameters,omitempty"`
}

type qwenImageGenerateInput struct {
	Messages []qwenImageGenerateMessage `json:"messages"`
}

type qwenImageGenerateMessage struct {
	Role    string                   `json:"role"`
	Content []map[string]any         `json:"content"`
}

type qwenImageGenerateResponse struct {
	Output struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role    string           `json:"role"`
				Content []map[string]any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	} `json:"output"`

	Usage struct {
		ImageCount int `json:"image_count"`
		Width      int `json:"width"`
		Height     int `json:"height"`
	} `json:"usage,omitempty"`

	RequestID string `json:"request_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (ig *ImageGenerateBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
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
		model = ig.config.Model
	}
	if model == "" {
		model = "qwen-image-2.0-pro"
	}

	parameters := map[string]any(nil)
	if input.ExtraParams != nil {
		// 允许调用方直接把 parameters 作为 ExtraParams["parameters"] 传进来
		if p, ok := input.ExtraParams["parameters"].(map[string]any); ok {
			parameters = p
		} else {
			parameters = input.ExtraParams
		}
	}

	reqBody := qwenImageGenerateRequest{
		Model: model,
		Input: qwenImageGenerateInput{
			Messages: []qwenImageGenerateMessage{
				{
					Role: "user",
					Content: []map[string]any{
						{"text": prompt},
					},
				},
			},
		},
		Parameters: parameters,
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", ig.getBaseURL(), bytes.NewReader(raw))
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ig.config.APIKey))
	httpReq.Header.Set("User-Agent", "PeopleAgent/1.0")

	resp, err := ig.client.Do(httpReq)
	if err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return &BrainOutput{Success: false, Error: string(body)}, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var decoded qwenImageGenerateResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return &BrainOutput{Success: false, Error: err.Error()}, err
	}
	if decoded.Code != "" {
		msg := decoded.Message
		if msg == "" {
			msg = decoded.Code
		}
		return &BrainOutput{Success: false, Error: msg, Metadata: map[string]any{"code": decoded.Code, "request_id": decoded.RequestID}}, fmt.Errorf("%s", msg)
	}

	if len(decoded.Output.Choices) == 0 || len(decoded.Output.Choices[0].Message.Content) == 0 {
		return &BrainOutput{Success: false, Error: "no image in response"}, fmt.Errorf("no image in response")
	}

	// content[0] 形如 {"image": "https://...png?..."}
	imageURL, _ := decoded.Output.Choices[0].Message.Content[0]["image"].(string)
	if strings.TrimSpace(imageURL) == "" {
		// 兜底：遍历找到第一个 image 字段
		for _, item := range decoded.Output.Choices[0].Message.Content {
			if v, ok := item["image"].(string); ok && strings.TrimSpace(v) != "" {
				imageURL = v
				break
			}
		}
	}
	if strings.TrimSpace(imageURL) == "" {
		return &BrainOutput{Success: false, Error: "no image url found in response"}, fmt.Errorf("no image url found in response")
	}

	return &BrainOutput{
		Success:  true,
		Mode:     BrainIMageGenerate,
		ImageURL: imageURL,
		Metadata: map[string]any{
			"request_id":  decoded.RequestID,
			"image_count": decoded.Usage.ImageCount,
			"width":       decoded.Usage.Width,
			"height":      decoded.Usage.Height,
			"model":       model,
		},
	}, nil
}

func (ig *ImageGenerateBrain) getBaseURL() string {
	// 允许用户手动覆盖（完整 URL）
	if ig.config.BaseURL != "" {
		return ig.config.BaseURL
	}
	if endpoint, ok := ig.config.APIEndpoints["image_generate"]; ok {
		return endpoint
	}

	// 文档同步接口：
	// 北京：https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation
	// 新加坡：https://dashscope-intl.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation
	switch ig.config.Region {
	case RegionSingapore:
		return "https://dashscope-intl.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	default:
		return "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	}
}

