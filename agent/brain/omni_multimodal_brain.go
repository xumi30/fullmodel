package brain

import (
	"fmt"
	"strings"

	"github.com/xumi30/fullmodel/utils/logging"
)

// OmniBrain 使用百炼 OpenAI 兼容通道调用 Qwen-Omni 系列：支持音频/图像/视频与文本的组合输入。
// 按产品约束，上游 HTTP 始终为 stream=true。
type OmniBrain struct {
	ib *ImageBrain
}

// NewOmniBrain 从「omni」类模型配置构造（参见 config/llm.yaml brains.omni）。
func NewOmniBrain(config *QwenConfig) *OmniBrain {
	return &OmniBrain{ib: NewImageBrain(config)}
}

// ProcessInput 实现 Brain：强制流式；默认 modalities=["text"]，可用 Extra 调整。
func (o *OmniBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return brainError("input is nil"), fmt.Errorf("input is nil")
	}
	if o.ib == nil {
		return brainError("omni brain is nil"), fmt.Errorf("omni brain is nil")
	}

	ctx := input.ContextOrBackground()
	messages := input.Messages
	if len(messages) == 0 {
		content, err := o.ib.buildUserContent(input)
		if err != nil {
			return brainError(err.Error()), err
		}
		messages = []Message{{Role: "user", Content: content}}
	}

	req := &ChatCompletionRequest{
		Model:         firstNonEmptyString(input.Options.Model, o.ib.config.Model, "qwen3.5-omni-plus"),
		Messages:      messages,
		Stream:        true,
		StreamOptions: &StreamOptions{IncludeUsage: true},
		Tools:         input.Tools,
		Temperature:   input.Options.Temperature,
		TopP:          input.Options.TopP,
		MaxTokens:     input.Options.MaxTokens,
		Modalities:    []string{"text"},
	}

	if ex := extraBodySansOmniKeys(input.Extra); len(ex) > 0 {
		req.ExtraBody = ex
	}

	if input.Extra != nil {
		if mm := stringSliceFromAny(input.Extra["omni_modalities"]); len(mm) > 0 {
			req.Modalities = mm
		}
		if outAudio, _ := input.Extra["omni_output_audio"].(bool); outAudio {
			voice := "Tina"
			format := "wav"
			if s, ok := input.Extra["omni_audio_voice"].(string); ok && strings.TrimSpace(s) != "" {
				voice = strings.TrimSpace(s)
			}
			if s, ok := input.Extra["omni_audio_format"].(string); ok && strings.TrimSpace(s) != "" {
				format = strings.TrimSpace(s)
			}
			req.Audio = &AudioOutputConfig{Voice: voice, Format: format}
			hasAudio := false
			for _, m := range req.Modalities {
				if m == "audio" {
					hasAudio = true
					break
				}
			}
			if !hasAudio {
				req.Modalities = append(append([]string{}, req.Modalities...), "audio")
			}
		}
	}

	logging.Info("[omni.brain] stream_request model=%s modalities=%v messages=%d audio_bytes=%d has_audio_url=%v tools=%d",
		req.Model, req.Modalities, len(req.Messages), len(input.Media.Audio.Data), strings.TrimSpace(input.Media.Audio.URL) != "", len(req.Tools))

	out, err := createChatCompletionStream(ctx, o.ib.client, o.ib.config, *req)
	if err != nil {
		return brainError(err.Error()), err
	}
	out.Mode = BrainModeOmni
	return out, nil
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extraBodySansOmniKeys(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	out := make(map[string]any)
	for k, v := range extra {
		switch k {
		case "omni_output_audio", "omni_modalities", "omni_audio_voice", "omni_audio_format":
			continue
		default:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringSliceFromAny(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		var s []string
		for _, e := range x {
			if str, ok := e.(string); ok && str != "" {
				s = append(s, str)
			}
		}
		return s
	default:
		return nil
	}
}
