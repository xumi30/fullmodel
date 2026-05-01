package main

import (
	"context"
	"fmt"
	"os"

	"people/internal/agent/brain"
)

// 运行方式：
//   export DASHSCOPE_API_KEY="sk-xxx"
//   go run ./examples/openai_image_generate
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "qwen-image-2.0-pro")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewImageGenerateBrain(cfg)

	out, err := b.ProcessInput(&brain.BrainInput{
		Mode:    brain.BrainIMageGenerate,
		Context: context.Background(),
		Text:    "一只坐着的橘黄色的猫，表情愉悦，活泼可爱，逼真准确。",
		ExtraParams: map[string]any{
			"size":            "2048*2048",
			"watermark":       false,
			"prompt_extend":   true,
			"negative_prompt": "低分辨率，低画质，肢体畸形，手指畸形，画面过饱和，蜡像感，人脸无细节，过度光滑，画面具有AI感。构图混乱。文字模糊，扭曲。",
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "call failed:", err)
		os.Exit(1)
	}

	fmt.Println("image_url:", out.ImageURL)
	if out.Metadata != nil {
		fmt.Println("request_id:", out.Metadata["request_id"])
	}
}

