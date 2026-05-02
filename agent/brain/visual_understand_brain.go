package brain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ImageBrain 实现图像问答（视觉理解）的 Brain 接口（OpenAI 兼容模式）
//
// 对应百炼视觉理解模型：messages[].content 支持 image_url + text 的多模态数组输入。
type ImageBrain struct {
	config *Config
	client *http.Client
}

// NewImageBrain 创建新的图像处理大脑
func NewImageBrain(config *QwenConfig) *ImageBrain {
	return &ImageBrain{
		config: config,
		client: &http.Client{
			Timeout: 180 * time.Second,
		},
	}
}

// ProcessInput 实现 Brain 接口
func (ib *ImageBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return brainError("input is nil"), fmt.Errorf("input is nil")
	}

	ctx := input.ContextOrBackground()

	// 1) 优先使用用户传入的 Messages（允许多轮、多图、多模态自定义）
	messages := input.Messages

	// 2) 若未传 Messages，则根据 Media/Prompt 构建一条 user 消息
	if len(messages) == 0 {
		content, err := ib.buildUserContent(input)
		if err != nil {
			return brainError(err.Error()), err
		}
		messages = []Message{{Role: "user", Content: content}}
	}

	req := &ChatCompletionRequest{
		Model:       input.Options.Model,
		Messages:    messages,
		Stream:      input.Options.Stream,
		Tools:       input.Tools,
		Temperature: input.Options.Temperature,
		TopP:        input.Options.TopP,
		MaxTokens:   input.Options.MaxTokens,
	}

	if req.Model == "" {
		req.Model = ib.config.Model
	}
	if req.Model == "" {
		// 视觉模型默认更合理的选择（与用户提供文档一致）
		req.Model = "qwen3.6-plus"
	}

	// 透传 Extra 到 extra_body（用于 enable_thinking、vl_high_resolution_images、max_pixels 等）
	if len(input.Extra) > 0 {
		req.ExtraBody = input.Extra
	}

	if input.Options.Stream {
		return ib.CreateChatCompletionStream(ctx, *req)
	}

	resp, err := ib.CreateChatCompletion(ctx, *req)
	if err != nil {
		return brainError(err.Error()), err
	}
	if len(resp.Choices) == 0 {
		return brainError("no response from model"), fmt.Errorf("no response from model")
	}

	content, ok := resp.Choices[0].Message.Content.(string)
	if !ok {
		// 兼容有些实现把 content 编成复杂结构时的兜底
		raw, _ := json.Marshal(resp.Choices[0].Message.Content)
		content = string(raw)
	}

	out := brainSuccess(ib.outputMode(input))
	out.Content.Text = content
	out.Content.Messages = []Message{{Role: "assistant", Content: content}}
	out.Choices = resp.Choices
	out.Usage = &Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	out.Metadata = map[string]any{
		"model": resp.Model,
		"id":    resp.ID,
	}
	return &out, nil
}

func (ib *ImageBrain) outputMode(input *BrainInput) BrainMode {
	// 显式指定优先
	if input.Mode == BrainModeVideoUnderstand {
		return BrainModeVideoUnderstand
	}
	if input.Mode == BrainModeImageUnderstand {
		return BrainModeImageUnderstand
	}

	// 根据输入推断
	if input.Media.Video.URL != "" || len(input.Media.Video.Data) > 0 {
		return BrainModeVideoUnderstand
	}
	for _, p := range input.Media.Parts {
		if p.Type == "video_url" || p.Type == "video" || p.VideoURL != nil || len(p.Video) > 0 || p.VideoData != nil {
			return BrainModeVideoUnderstand
		}
	}
	return BrainModeImageUnderstand
}

func (ib *ImageBrain) buildUserContent(input *BrainInput) ([]any, error) {
	var content []any

	// a) Media.Parts 优先（可以多图）
	for _, p := range input.Media.Parts {
		switch p.Type {
		case "image_url":
			if p.ImageURL != nil && p.ImageURL.URL != "" {
				content = append(content, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": p.ImageURL.URL,
					},
				})
			}
		case "text":
			// 延后统一追加，避免重复
		case "image_data":
			// 支持 ContentImageData（转成 data url）
			if p.ImageData != nil && len(p.ImageData.Data) > 0 {
				url := ib.buildDataURL(p.ImageData.MimeType, p.ImageData.Data)
				content = append(content, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": url,
					},
				})
			}
		case "video_url":
			if p.VideoURL != nil && p.VideoURL.URL != "" {
				item := map[string]any{
					"type": "video_url",
					"video_url": map[string]any{
						"url": p.VideoURL.URL,
					},
				}
				if p.VideoURL.FPS > 0 {
					item["fps"] = p.VideoURL.FPS
				} else if fps, ok := extractFPS(input.Extra); ok {
					item["fps"] = fps
				}
				content = append(content, item)
			}
		case "video":
			if len(p.Video) > 0 {
				item := map[string]any{
					"type":  "video",
					"video": p.Video,
				}
				if fps, ok := extractFPS(input.Extra); ok {
					item["fps"] = fps
				}
				content = append(content, item)
			}
		case "video_data":
			if p.VideoData != nil && len(p.VideoData.Data) > 0 {
				url := ib.buildDataURL(p.VideoData.MimeType, p.VideoData.Data)
				item := map[string]any{
					"type": "video_url",
					"video_url": map[string]any{
						"url": url,
					},
				}
				if fps, ok := extractFPS(input.Extra); ok {
					item["fps"] = fps
				}
				content = append(content, item)
			}
		default:
			// 其他类型（audio 等）这里不处理
		}
	}

	// b) 兼容单图 URL
	if input.Media.Image.URL != "" {
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": input.Media.Image.URL,
			},
		})
	}

	// c) 兼容二进制图像（转 data url）
	if len(input.Media.Image.Data) > 0 {
		url := ib.buildDataURL(input.Media.Image.MimeType, input.Media.Image.Data)
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": url,
			},
		})
	}

	// d) 兼容视频 URL
	if input.Media.Video.URL != "" {
		item := map[string]any{
			"type": "video_url",
			"video_url": map[string]any{
				"url": input.Media.Video.URL,
			},
		}
		if fps, ok := extractFPS(input.Extra); ok {
			item["fps"] = fps
		}
		content = append(content, item)
	}

	// e) 兼容视频二进制（转 data url）
	if len(input.Media.Video.Data) > 0 {
		url := ib.buildDataURL(input.Media.Video.MimeType, input.Media.Video.Data)
		item := map[string]any{
			"type": "video_url",
			"video_url": map[string]any{
				"url": url,
			},
		}
		if fps, ok := extractFPS(input.Extra); ok {
			item["fps"] = fps
		}
		content = append(content, item)
	}

	if len(content) == 0 {
		return nil, fmt.Errorf("no vision input provided (need image/video url/data or multimodal_parts)")
	}

	// f) 文本提示放最后（若为空，给一个默认提示）
	text := strings.TrimSpace(input.Prompt)
	if text == "" {
		if ib.outputMode(input) == BrainModeVideoUnderstand {
			text = "请描述这段视频的内容，并回答我关于视频的问题。"
		} else {
			text = "请描述图片内容，并回答我关于图片的问题。"
		}
	}
	content = append(content, map[string]any{
		"type": "text",
		"text": text,
	})

	return content, nil
}

func (ib *ImageBrain) buildDataURL(mime string, data []byte) string {
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mime, b64)
}

// CreateChatCompletion 创建聊天完成 (非流式)
func (ib *ImageBrain) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return createChatCompletion(ctx, ib.client, ib.config, req)
}

// CreateChatCompletionStream 创建流式聊天完成
func (ib *ImageBrain) CreateChatCompletionStream(ctx context.Context, req ChatCompletionRequest) (*BrainOutput, error) {
	out, err := createChatCompletionStream(ctx, ib.client, ib.config, req)
	if err != nil {
		return nil, err
	}
	// 流式模式下把 mode 与输入保持一致更合理；这里用 req.Messages 推断不可靠，
	// 但至少不强制为 image，交由调用方根据需要自行处理。
	out.Mode = BrainModeStream
	return out, nil
}

func extractFPS(extra map[string]any) (float64, bool) {
	if extra == nil {
		return 0, false
	}
	// 允许用户通过 Extra 传 fps（float64/int/string 都尽量兼容）
	if v, ok := extra["fps"]; ok {
		switch t := v.(type) {
		case float64:
			return t, t > 0
		case float32:
			f := float64(t)
			return f, f > 0
		case int:
			f := float64(t)
			return f, f > 0
		case int64:
			f := float64(t)
			return f, f > 0
		case json.Number:
			f, err := t.Float64()
			if err == nil && f > 0 {
				return f, true
			}
		case string:
			if n, err := json.Number(t).Float64(); err == nil && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}
