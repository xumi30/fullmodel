package runtime

import (
	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/processmessage"
	"github.com/xumi30/fullmodel/utils/fileop"
)

func NewRegistryFromConfigs(cfgs *fileop.BrainConfigs) (*Registry, error) {
	textCfg, err := cfgs.Config(fileop.BrainConfigText)
	if err != nil {
		return nil, err
	}
	visionCfg, err := cfgs.Config(fileop.BrainConfigVision)
	if err != nil {
		return nil, err
	}
	voiceCfg, err := cfgs.Config(fileop.BrainConfigVoice)
	if err != nil {
		return nil, err
	}
	imageCfg, err := cfgs.Config(fileop.BrainConfigImage)
	if err != nil {
		return nil, err
	}

	textBrain := brain.NewTextBrain(ToBrainConfig(textCfg))
	visionBrain := brain.NewImageBrain(ToBrainConfig(visionCfg))
	asrBrain := brain.NewSpeech2TxtASRBrain(ToBrainConfig(voiceCfg))
	voiceBrain := brain.NewText2VoiceBrain(ToBrainConfig(voiceCfg))
	imageBrain := brain.NewImageGenerateBrain(ToBrainConfig(imageCfg))
	imageEditBrain := brain.NewImageEditBrain(ToBrainConfig(imageCfg))
	textVideoBrain := brain.NewVideoTextGenerateBrain(ToBrainConfig(imageCfg))
	imageVideoBrain := brain.NewImage2VideoGenerateBrain(ToBrainConfig(imageCfg))

	registry := NewRegistry()
	registrations := []struct {
		kind        processmessage.Kind
		brain       brain.Brain
		name        string
		description string
		streaming   bool
	}{
		{processmessage.KindText, textBrain, "Text", "One-shot text generation", true},
		{processmessage.KindChat, textBrain, "Chat", "Multi-turn text chat", true},
		{processmessage.KindImage, visionBrain, "Image understanding", "Analyze an image with a prompt", true},
		{processmessage.KindVideo, visionBrain, "Video understanding", "Analyze a video with a prompt", true},
		{processmessage.KindMultimodal, visionBrain, "Multimodal understanding", "Analyze explicit multimodal content parts", true},
		{processmessage.KindSpeechToText, asrBrain, "Speech to text", "Transcribe audio bytes", false},
		{processmessage.KindTextToSpeech, voiceBrain, "Text to speech", "Synthesize audio from text", false},
		{processmessage.KindImageGenerate, imageBrain, "Image generation", "Generate an image from a prompt", false},
		{processmessage.KindImageEdit, imageEditBrain, "Image editing", "Edit one or more images from an instruction", false},
		{processmessage.KindTextToVideo, textVideoBrain, "Text to video", "Generate a video from a prompt", false},
		{processmessage.KindImageToVideo, imageVideoBrain, "Image to video", "Generate a video from a first frame image", false},
	}

	for _, item := range registrations {
		if err := registry.Register(item.kind, item.brain, Capability{
			Kind:        item.kind,
			Name:        item.name,
			Description: item.description,
			Streaming:   item.streaming,
		}); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func ToBrainConfig(cfg *fileop.ModelConfig) *brain.QwenConfig {
	if cfg == nil {
		return &brain.QwenConfig{}
	}
	return &brain.QwenConfig{
		APIKey:       cfg.APIKey,
		BaseURL:      cfg.BaseURL,
		Model:        cfg.Model,
		Region:       cfg.Region,
		Provider:     brain.ProviderType(cfg.Provider),
		APIEndpoints: cloneEndpoints(cfg.APIEndpoints),
	}
}

func cloneEndpoints(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
