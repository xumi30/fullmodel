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
//	go run ./examples/openai_image_brain
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "qwen3.6-flash")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewImageBrain(cfg)

	out, err := b.ProcessInput(&brain.BrainInput{
		Mode:     brain.BrainModeImageUnderstand, // 视觉理解统一入口：图片/视频都可用
		Context:  context.Background(),
		ImageURL: "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20241022/emyrja/dog_and_girl.jpeg",
		Text:     "图中描绘的是什么景象？",
		ExtraParams: map[string]any{
			// 可选：高分辨率细节增强（对 OCR/小目标更有效，成本更高）
			"vl_high_resolution_images": true,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "call failed:", err)
		os.Exit(1)
	}

	fmt.Println(out.Text)
}
