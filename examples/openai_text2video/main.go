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
//   go run ./examples/openai_text2video
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "happyhorse-1.0-t2v")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewVideoTextGenerateBrain(cfg)

	// 建议给文生视频设置超时（例如 10 分钟）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	out, err := b.ProcessInput(&brain.BrainInput{
		Mode:    brain.BrainText2VideoGenerate,
		Context: ctx,
		Text:    "一座由硬纸板和瓶盖搭建的微型城市，在夜晚焕发出生机。一列硬纸板火车缓缓驶过，小灯点缀其间，照亮前路。",
		ExtraParams: map[string]any{
			"resolution":        "1080P",
			"ratio":             "16:9",
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

