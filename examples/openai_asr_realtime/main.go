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
//   curl -L -o asr_example.wav https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20241114/mgiguo/asr_example.wav
//   go run ./examples/openai_asr_realtime
func main() {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing env DASHSCOPE_API_KEY")
		os.Exit(1)
	}

	audio, err := os.ReadFile("asr_example.wav")
	if err != nil {
		fmt.Fprintln(os.Stderr, "read asr_example.wav failed:", err)
		os.Exit(1)
	}

	cfg := brain.DefaultQwenConfig(apiKey, "fun-asr-realtime")
	cfg.Provider = brain.ProviderQwen
	cfg.Region = brain.RegionBeijing

	b := brain.NewSpeech2TxtASRBrain(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	out, err := b.ProcessInput(&brain.BrainInput{
		Mode:      brain.BrainModeASR,
		Context:   ctx,
		AudioData: audio,
		ExtraParams: map[string]any{
			"format":      "wav",
			"sample_rate": 16000,
			// 可选：更贴近实时的发送节奏
			"chunk_size":        1024,
			"chunk_interval_ms": 100,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "asr failed:", err)
		os.Exit(1)
	}

	fmt.Println(out.Text)
}

