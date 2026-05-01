package main

import (
	"context"
	"fmt"
	"os"

	"people/internal/agent/brain"
)

// 运行方式：
//
//	export DASHSCOPE_API_KEY="sk-xxx"
//	go run ./examples/openai_video_brain
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "qwen3.6-flash")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewImageBrain(cfg) // 视觉理解统一入口：图片/视频都可用

	out, err := b.ProcessInput(&brain.BrainInput{
		Mode:     brain.BrainModeVideoUnderstand,
		Context:  context.Background(),
		VideoURL: "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20241115/cqqkru/1.mp4",
		Text:     "这段视频的内容是什么？",
		ExtraParams: map[string]any{
			// 可选：抽帧频率（秒/帧的倒数），官方示例常用 2
			"fps": 2,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "call failed:", err)
		os.Exit(1)
	}

	fmt.Println(out.Text)
}
