package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"people/internal/agent/brain"
)

// 运行方式：
//
//	export DASHSCOPE_API_KEY="sk-xxx"
//	go run ./examples/openai_tts_cosyvoice
//
// 输出：output.mp3（注意：mp3 流式合成只有首帧包含头信息，后续帧为纯音频数据，需整体拼接后再播放）
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "cosyvoice-v3-flash")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewText2VoiceBrain(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	out, err := b.ProcessInput(&brain.BrainInput{
		Mode:    brain.BrainModeVoiceGenerate,
		Context: ctx,
		Text:    "床前明月光，疑是地上霜。举头望明月，低头思故乡。",
		ExtraParams: map[string]any{
			"voice":       "longanyang",
			"format":      "mp3",
			"sample_rate": 22050,
			"volume":      50,
			"rate":        1.0,
			"pitch":       1.0,
			"enable_ssml": false,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tts failed:", err)
		os.Exit(1)
	}

	if err := os.WriteFile("output.mp3", out.AudioData, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write output.mp3 failed:", err)
		os.Exit(1)
	}

	fmt.Println("saved:", "output.mp3")
	if out.Metadata != nil {
		fmt.Println("task_id:", out.Metadata["task_id"])
		fmt.Println("request_uuid:", out.Metadata["request_uuid"])
	}
}
