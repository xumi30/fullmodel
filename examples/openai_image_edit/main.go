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
//   go run ./examples/openai_image_edit
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "qwen-image-2.0-pro")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewImageEditBrain(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	out, err := b.ProcessInput(&brain.BrainInput{
		Context:  ctx,
		ImageURL: "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20260310/jiydyi/image+%2818%29-2026-03-10-16-39-59.webp",
		Text:     "在画面右下角石板路旁、靠近树干根部的位置，以浅灰墨色手写体题写一首七言绝句，字体为行楷风格，笔触自然流畅、略带飞白，大小适中（约占画面高度1/10），与整体水墨淡雅氛围协调。诗文内容为：“青石桥畔柳风轻， 素手拈花闭目听。 一水碧痕浮旧梦， 半篙烟雨入空舲。”诗句横向排列，四句分两行书写（前两句一行，后两句一行），末句“舲”字右下角钤一枚朱红小印，印文为“江南”二字篆书，尺寸约等于单字高度的1/3。",
		ExtraParams: map[string]any{
			"n":               1,
			"watermark":       false,
			"negative_prompt": " ",
			"prompt_extend":   true,
			"size":            "2048*2048",
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "image edit failed:", err)
		os.Exit(1)
	}

	fmt.Println("image_url:", out.ImageURL)
	if out.Metadata != nil {
		fmt.Println("request_id:", out.Metadata["request_id"])
		if imgs, ok := out.Metadata["images"]; ok {
			fmt.Println("images:", imgs)
		}
	}
}

