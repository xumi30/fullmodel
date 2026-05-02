package brain

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ImageEditBrain 图像编辑/融合（Qwen-Image Edit，同步接口）
//
// 文档：POST /api/v1/services/aigc/multimodal-generation/generation
// 请求体：
//
//	{
//	  "model": "qwen-image-2.0-pro",
//	  "input": {"messages":[{"role":"user","content":[{"image":"..."} , {"text":"..."}]}]},
//	  "parameters": {...}
//	}
//
// 响应体：output.choices[0].message.content 中包含 1-6 个 {"image": "...url..."}（URL 24小时有效）
type ImageEditBrain struct {
	config *Config
	client *http.Client
}

func NewImageEditBrain(config *QwenConfig) *ImageEditBrain {
	return &ImageEditBrain{
		config: config,
		client: &http.Client{
			Timeout: 180 * time.Second,
		},
	}
}

type qwenImageEditRequest struct {
	Model      string             `json:"model"`
	Input      qwenImageEditInput `json:"input"`
	Parameters map[string]any     `json:"parameters,omitempty"`
}

type qwenImageEditInput struct {
	Messages []qwenImageEditMessage `json:"messages"`
}

type qwenImageEditMessage struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

type qwenImageEditResponse struct {
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

func (ib *ImageEditBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return brainError("input is nil"), fmt.Errorf("input is nil")
	}

	ctx := input.ContextOrBackground()

	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return brainError("missing edit instruction (BrainInput.Prompt)"), fmt.Errorf("missing edit instruction (BrainInput.Prompt)")
	}

	images, err := ib.extractInputImages(input)
	if err != nil {
		return brainError(err.Error()), err
	}
	if len(images) < 1 || len(images) > 3 {
		return brainError("image edit requires 1-3 input images"), fmt.Errorf("image edit requires 1-3 input images")
	}

	model := input.Options.Model
	if model == "" {
		model = ib.config.Model
	}
	if model == "" {
		// 文档示例用 qwen-image-2.0-pro 做编辑
		model = "qwen-image-2.0-pro"
	}

	parameters := map[string]any(nil)
	if input.Extra != nil {
		// 允许调用方直接把 parameters 作为 Extra["parameters"] 传进来
		if p, ok := input.Extra["parameters"].(map[string]any); ok {
			parameters = p
		} else {
			parameters = input.Extra
		}
	}

	content := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		content = append(content, map[string]any{"image": img})
	}
	content = append(content, map[string]any{"text": prompt}) // 必须且仅能有一个 text

	reqBody := qwenImageEditRequest{
		Model: model,
		Input: qwenImageEditInput{
			Messages: []qwenImageEditMessage{
				{
					Role:    "user",
					Content: content,
				},
			},
		},
		Parameters: parameters,
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return brainError(err.Error()), err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", ib.getBaseURL(), bytes.NewReader(raw))
	if err != nil {
		return brainError(err.Error()), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ib.config.APIKey))
	httpReq.Header.Set("User-Agent", "PeopleAgent/1.0")

	resp, err := ib.client.Do(httpReq)
	if err != nil {
		return brainError(err.Error()), err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return brainError(string(body)), fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var decoded qwenImageEditResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return brainError(err.Error()), err
	}
	if decoded.Code != "" {
		msg := decoded.Message
		if msg == "" {
			msg = decoded.Code
		}
		return brainErrorWithMetadata(msg, map[string]any{"code": decoded.Code, "request_id": decoded.RequestID}), fmt.Errorf("%s", msg)
	}

	if len(decoded.Output.Choices) == 0 {
		return brainError("no choices in response"), fmt.Errorf("no choices in response")
	}

	outImages := extractImagesFromContent(decoded.Output.Choices[0].Message.Content)
	if len(outImages) == 0 {
		return brainError("no image url found in response"), fmt.Errorf("no image url found in response")
	}

	meta := map[string]any{
		"request_id": decoded.RequestID,
		"model":      model,
		"image_count": func() int {
			if decoded.Usage.ImageCount > 0 {
				return decoded.Usage.ImageCount
			}
			return len(outImages)
		}(),
		"width":  decoded.Usage.Width,
		"height": decoded.Usage.Height,
		"images": outImages,
		"op":     "image_edit",
	}

	out := brainSuccess(BrainIMageGenerate)
	out.Content.Image.URL = outImages[0]
	out.Metadata = meta
	return &out, nil
}

func (ib *ImageEditBrain) extractInputImages(input *BrainInput) ([]string, error) {
	var images []string

	// 1) Media.Parts 里的 image_url / image_data
	for _, p := range input.Media.Parts {
		switch p.Type {
		case "image_url":
			if p.ImageURL != nil && strings.TrimSpace(p.ImageURL.URL) != "" {
				images = append(images, p.ImageURL.URL)
			}
		case "image_data":
			if p.ImageData != nil && len(p.ImageData.Data) > 0 {
				images = append(images, buildImageDataURL(p.ImageData.MimeType, p.ImageData.Data))
			}
		}
	}

	// 2) 单图 Media.Image
	if strings.TrimSpace(input.Media.Image.URL) != "" {
		images = append(images, input.Media.Image.URL)
	}
	if len(input.Media.Image.Data) > 0 {
		images = append(images, buildImageDataURL(input.Media.Image.MimeType, input.Media.Image.Data))
	}

	if len(images) == 0 {
		return nil, fmt.Errorf("no input images provided (need BrainInput.Media.Image or media.parts)")
	}
	return images, nil
}

func (ib *ImageEditBrain) getBaseURL() string {
	if ib.config.BaseURL != "" {
		return ib.config.BaseURL
	}
	if endpoint, ok := ib.config.APIEndpoints["image_edit"]; ok {
		return endpoint
	}
	switch ib.config.Region {
	case RegionSingapore:
		return "https://dashscope-intl.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	default:
		return "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	}
}

func buildImageDataURL(mime string, data []byte) string {
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mime, b64)
}

func extractImagesFromContent(content []map[string]any) []string {
	var out []string
	for _, item := range content {
		if v, ok := item["image"].(string); ok && strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}
