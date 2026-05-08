package processmessage

import (
	"context"
	"fmt"
	"strings"

	"github.com/xumi30/fullmodel/agent/brain"
)

// Kind 描述外部输入消息的业务类型。
type Kind string

const (
	KindText          Kind = "text"
	KindChat          Kind = "chat"
	KindImage         Kind = "image"
	KindVideo         Kind = "video"
	KindSpeechToText  Kind = "speech_to_text"
	KindTextToSpeech  Kind = "text_to_speech"
	KindImageGenerate Kind = "image_generate"
	KindImageEdit     Kind = "image_edit"
	KindTextToVideo   Kind = "text_to_video"
	KindImageToVideo  Kind = "image_to_video"
	KindMultimodal    Kind = "multimodal"
)

// Message 是 processmessage 包对外的统一消息接口。
type Message interface {
	MessageKind() Kind
}

// BrainSelector 根据消息类型选择实际处理它的 brain。
type BrainSelector interface {
	SelectBrain(kind Kind) (brain.Brain, bool)
}

// BrainSelectorFunc 让普通函数可以作为 BrainSelector 使用。
type BrainSelectorFunc func(kind Kind) (brain.Brain, bool)

func (fn BrainSelectorFunc) SelectBrain(kind Kind) (brain.Brain, bool) {
	return fn(kind)
}

// Options 是一次消息处理的通用调用参数。
type Options struct {
	Context context.Context
	Tools   []brain.Tool
	Model   string
	Stream  bool

	// DisableDefaultTools prevents the runtime from injecting the client's
	// default tool executor when Options.Tools is empty.
	DisableDefaultTools bool

	Temperature *float64
	TopP        *float64
	MaxTokens   *int

	Extra map[string]any
}

// Processor 把各种外部消息规范化为 brain.BrainInput，也可以直接调用 brain。
type Processor interface {
	BuildInput(message Message, options Options) (*brain.BrainInput, error)
	Process(message Message, options Options) (*brain.BrainOutput, error)
}

type DefaultProcessor struct {
	selector BrainSelector
}

func NewProcessor(selector BrainSelector) *DefaultProcessor {
	return &DefaultProcessor{selector: selector}
}

func (p *DefaultProcessor) BuildInput(message Message, options Options) (*brain.BrainInput, error) {
	if message == nil {
		return nil, fmt.Errorf("message is nil")
	}

	input := newInput(message.MessageKind(), options)
	switch msg := message.(type) {
	case TextMessage:
		applyTextMessage(input, msg)
	case *TextMessage:
		applyTextMessage(input, *msg)
	case ChatMessage:
		applyChatMessage(input, msg)
	case *ChatMessage:
		applyChatMessage(input, *msg)
	case ImageMessage:
		applyImageMessage(input, msg)
	case *ImageMessage:
		applyImageMessage(input, *msg)
	case VideoMessage:
		applyVideoMessage(input, msg)
	case *VideoMessage:
		applyVideoMessage(input, *msg)
	case SpeechToTextMessage:
		applySpeechToTextMessage(input, msg)
	case *SpeechToTextMessage:
		applySpeechToTextMessage(input, *msg)
	case TextToSpeechMessage:
		applyTextToSpeechMessage(input, msg)
	case *TextToSpeechMessage:
		applyTextToSpeechMessage(input, *msg)
	case ImageGenerateMessage:
		applyImageGenerateMessage(input, msg)
	case *ImageGenerateMessage:
		applyImageGenerateMessage(input, *msg)
	case ImageEditMessage:
		applyImageEditMessage(input, msg)
	case *ImageEditMessage:
		applyImageEditMessage(input, *msg)
	case TextToVideoMessage:
		applyTextToVideoMessage(input, msg)
	case *TextToVideoMessage:
		applyTextToVideoMessage(input, *msg)
	case ImageToVideoMessage:
		applyImageToVideoMessage(input, msg)
	case *ImageToVideoMessage:
		applyImageToVideoMessage(input, *msg)
	case MultimodalMessage:
		applyMultimodalMessage(input, msg)
	case *MultimodalMessage:
		applyMultimodalMessage(input, *msg)
	default:
		return nil, fmt.Errorf("unsupported message type %T", message)
	}

	if err := validateInput(input, message.MessageKind()); err != nil {
		return nil, err
	}
	return input, nil
}

func (p *DefaultProcessor) Process(message Message, options Options) (*brain.BrainOutput, error) {
	if p == nil || p.selector == nil {
		return nil, fmt.Errorf("brain selector is nil")
	}

	input, err := p.BuildInput(message, options)
	if err != nil {
		return nil, err
	}

	processor, ok := p.selector.SelectBrain(message.MessageKind())
	if !ok || processor == nil {
		return nil, fmt.Errorf("no brain registered for message kind %q", message.MessageKind())
	}
	return processor.ProcessInput(input)
}

// TextMessage 表示普通单轮文本请求。
type TextMessage struct {
	Text     string
	History  []brain.Message
	System   string
	Messages []brain.Message
}

func (TextMessage) MessageKind() Kind { return KindText }

// ChatMessage 表示调用方已经组织好的多轮对话。
type ChatMessage struct {
	Messages []brain.Message
}

func (ChatMessage) MessageKind() Kind { return KindChat }

// ImageMessage 表示图片理解请求。
type ImageMessage struct {
	Prompt string
	Image  brain.MediaResource
	Parts  []brain.ContentPart
}

func (ImageMessage) MessageKind() Kind { return KindImage }

// VideoMessage 表示视频理解请求。
type VideoMessage struct {
	Prompt string
	Video  brain.MediaResource
	Parts  []brain.ContentPart
}

func (VideoMessage) MessageKind() Kind { return KindVideo }

// SpeechToTextMessage 表示语音识别请求。
type SpeechToTextMessage struct {
	Audio brain.MediaResource
}

func (SpeechToTextMessage) MessageKind() Kind { return KindSpeechToText }

// TextToSpeechMessage 表示文本转语音请求。
type TextToSpeechMessage struct {
	Text string
	// Voice is the provider-specific TTS voice ID, such as longxiaochun_v3.
	Voice string
	// Format is the output audio format, such as mp3, wav, or pcm.
	Format string
	// SampleRate is the output sample rate in Hz.
	SampleRate int
	// Volume controls synthesis volume when supported by the provider.
	Volume int
	// Rate controls speaking speed when supported by the provider.
	Rate float64
	// Pitch controls voice pitch when supported by the provider.
	Pitch float64
	// EnableSSML tells the provider to treat Text as SSML when supported.
	EnableSSML *bool
	// Extra carries provider-specific TTS parameters not modeled above.
	Extra map[string]any
}

func (TextToSpeechMessage) MessageKind() Kind { return KindTextToSpeech }

// ImageGenerateMessage 表示文生图请求。
type ImageGenerateMessage struct {
	Prompt string
}

func (ImageGenerateMessage) MessageKind() Kind { return KindImageGenerate }

// ImageEditMessage 表示图像编辑请求。
type ImageEditMessage struct {
	Prompt string
	Images []brain.MediaResource
	Parts  []brain.ContentPart
}

func (ImageEditMessage) MessageKind() Kind { return KindImageEdit }

// TextToVideoMessage 表示文生视频请求。
type TextToVideoMessage struct {
	Prompt string
}

func (TextToVideoMessage) MessageKind() Kind { return KindTextToVideo }

// ImageToVideoMessage 表示图生视频请求。
type ImageToVideoMessage struct {
	Prompt     string
	FirstFrame brain.MediaResource
	Parts      []brain.ContentPart
}

func (ImageToVideoMessage) MessageKind() Kind { return KindImageToVideo }

// MultimodalMessage 表示调用方直接传入 ContentPart 的多模态请求。
type MultimodalMessage struct {
	Mode   brain.BrainMode
	Prompt string
	Parts  []brain.ContentPart
}

func (MultimodalMessage) MessageKind() Kind { return KindMultimodal }

func newInput(kind Kind, options Options) *brain.BrainInput {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}

	return &brain.BrainInput{
		Mode:    modeForKind(kind),
		Context: ctx,
		Tools:   options.Tools,
		Options: brain.BrainOptions{
			Model:       options.Model,
			Stream:      options.Stream,
			Temperature: options.Temperature,
			TopP:        options.TopP,
			MaxTokens:   options.MaxTokens,
		},
		Extra: options.Extra,
	}
}

func modeForKind(kind Kind) brain.BrainMode {
	switch kind {
	case KindImage:
		return brain.BrainModeImageUnderstand
	case KindVideo:
		return brain.BrainModeVideoUnderstand
	case KindSpeechToText:
		return brain.BrainModeASR
	case KindTextToSpeech:
		return brain.BrainModeVoiceGenerate
	case KindImageGenerate, KindImageEdit:
		return brain.BrainIMageGenerate
	case KindTextToVideo:
		return brain.BrainText2VideoGenerate
	case KindImageToVideo:
		return brain.BrainImage2VideoGenerate
	case KindMultimodal:
		return brain.BrainModeMultimodal
	default:
		return brain.BrainModeText
	}
}

func applyTextMessage(input *brain.BrainInput, message TextMessage) {
	if len(message.Messages) > 0 {
		input.Messages = message.Messages
		return
	}

	messages := make([]brain.Message, 0, len(message.History)+2)
	if strings.TrimSpace(message.System) != "" {
		messages = append(messages, brain.NewSystemMessage(message.System))
	}
	messages = append(messages, message.History...)
	if strings.TrimSpace(message.Text) != "" {
		messages = append(messages, brain.NewUserMessage(message.Text))
	}

	input.Prompt = message.Text
	input.Messages = messages
}

func applyChatMessage(input *brain.BrainInput, message ChatMessage) {
	input.Messages = message.Messages
}

func applyImageMessage(input *brain.BrainInput, message ImageMessage) {
	input.Prompt = message.Prompt
	input.Media.Image = message.Image
	input.Media.Parts = append(input.Media.Parts, message.Parts...)
}

func applyVideoMessage(input *brain.BrainInput, message VideoMessage) {
	input.Prompt = message.Prompt
	input.Media.Video = message.Video
	input.Media.Parts = append(input.Media.Parts, message.Parts...)
}

func applySpeechToTextMessage(input *brain.BrainInput, message SpeechToTextMessage) {
	input.Media.Audio = message.Audio
}

func applyTextToSpeechMessage(input *brain.BrainInput, message TextToSpeechMessage) {
	input.Prompt = message.Text
	extra := cloneExtra(message.Extra)
	if strings.TrimSpace(message.Voice) != "" {
		extra["voice"] = strings.TrimSpace(message.Voice)
	}
	if strings.TrimSpace(message.Format) != "" {
		extra["format"] = strings.TrimSpace(message.Format)
	}
	if message.SampleRate > 0 {
		extra["sample_rate"] = message.SampleRate
	}
	if message.Volume > 0 {
		extra["volume"] = message.Volume
	}
	if message.Rate != 0 {
		extra["rate"] = message.Rate
	}
	if message.Pitch != 0 {
		extra["pitch"] = message.Pitch
	}
	if message.EnableSSML != nil {
		extra["enable_ssml"] = *message.EnableSSML
	}
	if len(extra) > 0 {
		input.Extra = extra
	}
}

func applyImageGenerateMessage(input *brain.BrainInput, message ImageGenerateMessage) {
	input.Prompt = message.Prompt
}

func applyImageEditMessage(input *brain.BrainInput, message ImageEditMessage) {
	input.Prompt = message.Prompt
	input.Media.Parts = append(input.Media.Parts, message.Parts...)
	if len(message.Images) > 0 {
		input.Media.Image = message.Images[0]
	}
	for _, image := range message.Images[1:] {
		input.Media.Parts = append(input.Media.Parts, imageContentPart(image))
	}
}

func applyTextToVideoMessage(input *brain.BrainInput, message TextToVideoMessage) {
	input.Prompt = message.Prompt
}

func applyImageToVideoMessage(input *brain.BrainInput, message ImageToVideoMessage) {
	input.Prompt = message.Prompt
	input.Media.Image = message.FirstFrame
	input.Media.Parts = append(input.Media.Parts, message.Parts...)
}

func applyMultimodalMessage(input *brain.BrainInput, message MultimodalMessage) {
	if message.Mode != "" {
		input.Mode = message.Mode
	}
	input.Prompt = message.Prompt
	input.Media.Parts = append(input.Media.Parts, message.Parts...)
}

func validateInput(input *brain.BrainInput, kind Kind) error {
	switch kind {
	case KindText:
		if len(input.Messages) == 0 && strings.TrimSpace(input.Prompt) == "" {
			return fmt.Errorf("text message is empty")
		}
	case KindChat:
		if len(input.Messages) == 0 {
			return fmt.Errorf("chat message requires at least one message")
		}
	case KindImage:
		if !hasMedia(input.Media.Image) && len(input.Media.Parts) == 0 {
			return fmt.Errorf("image message requires image resource or parts")
		}
	case KindVideo:
		if !hasMedia(input.Media.Video) && len(input.Media.Parts) == 0 {
			return fmt.Errorf("video message requires video resource or parts")
		}
	case KindSpeechToText:
		if len(input.Media.Audio.Data) == 0 {
			return fmt.Errorf("speech_to_text message requires audio data")
		}
	case KindTextToSpeech, KindImageGenerate, KindTextToVideo:
		if strings.TrimSpace(input.Prompt) == "" {
			return fmt.Errorf("%s message requires prompt", kind)
		}
	case KindImageEdit, KindImageToVideo:
		if strings.TrimSpace(input.Prompt) == "" {
			return fmt.Errorf("%s message requires prompt", kind)
		}
		if !hasMedia(input.Media.Image) && len(input.Media.Parts) == 0 {
			return fmt.Errorf("%s message requires image resource or parts", kind)
		}
	case KindMultimodal:
		if strings.TrimSpace(input.Prompt) == "" && len(input.Media.Parts) == 0 {
			return fmt.Errorf("multimodal message requires prompt or parts")
		}
	}
	return nil
}

func hasMedia(resource brain.MediaResource) bool {
	return strings.TrimSpace(resource.URL) != "" || len(resource.Data) > 0
}

func cloneExtra(extra map[string]any) map[string]any {
	out := make(map[string]any, len(extra))
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func imageContentPart(resource brain.MediaResource) brain.ContentPart {
	if strings.TrimSpace(resource.URL) != "" {
		return brain.NewContentPart("image_url", &brain.ContentImageURL{URL: resource.URL})
	}
	return brain.NewContentPart("image_data", &brain.ContentImageData{
		Data:     resource.Data,
		MimeType: resource.MimeType,
	})
}
