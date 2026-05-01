package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"people/internal/agent/brain"
)

// 运行方式：
//   export DASHSCOPE_API_KEY="sk-xxx"
//   go run ./examples/openai_image2video
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "happyhorse-1.0-i2v")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewImage2VideoGenerateBrain(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	out, err := b.ProcessInput(&brain.BrainInput{
		Mode:     brain.BrainImage2VideoGenerate,
		Context:  ctx,
		ImageURL: "https://cdn.translate.alibaba.com/r/wanx-demo-1.png",
		Text:     "一只猫在草地上奔跑",
		ExtraParams: map[string]any{
			"resolution":        "720P",
			"duration":          5,
			"watermark":         true,
			"poll_interval_sec": 15,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "call failed:", err)
		os.Exit(1)
	}

	fmt.Println("video_url:", out.VideoURL)
	if out.Metadata != nil {
		fmt.Println("task_id:", out.Metadata["task_id"])
		fmt.Println("request_id:", out.Metadata["request_id"])
	}
}

