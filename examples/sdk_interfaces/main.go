package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xumi30/fullmodel"
	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/processmessage"
)

type check struct {
	name string
	run  func(context.Context) error
}

type weatherArgs struct {
	City string `json:"city"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client, err := fullmodel.Open(fullmodel.WithTools(weatherTool()))
	if err != nil {
		fatal("Open", err)
	}

	checks := []check{
		{name: "Capabilities", run: runCapabilities(client)},
		{name: "Tools", run: runTools(client)},
		{name: "Text", run: runText(client)},
		{name: "Run", run: runRawRun(client)},
		{name: "StreamText", run: runStreamText(client)},
		{name: "TextStream", run: runTextStreamAlias(client)},
		{name: "ChatMemory", run: runChatMemory(client)},
		{name: "ManualMemory", run: runManualMemory(client)},
		{name: "ToolLoop", run: runToolLoop(client)},
		{name: "ExecuteTool", run: runExecuteTool(client)},
	}

	if os.Getenv("FULLMODEL_EXAMPLE_MEDIA") == "1" {
		checks = append(checks,
			check{name: "TTS", run: runTTS(client)},
			check{name: "GenerateImage", run: runGenerateImage(client)},
			check{name: "TextToVideo", run: runTextToVideo(client)},
		)
	}

	var failed bool
	for _, c := range checks {
		fmt.Printf("\n===== %s =====\n", c.name)
		if err := c.run(ctx); err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "[FAIL] %s: %v\n", c.name, err)
			continue
		}
		fmt.Printf("[PASS] %s\n", c.name)
	}

	if failed {
		os.Exit(1)
	}
}

func weatherTool() fullmodel.SDKTool {
	return fullmodel.NewTool(
		"example_weather",
		"查询指定城市的示例天气。只要用户询问示例天气，就必须调用这个工具。",
		fullmodel.ObjectSchema(map[string]any{
			"city": map[string]any{
				"type":        "string",
				"description": "城市名，例如 Hangzhou",
			},
		}, "city"),
		func(ctx context.Context, raw string) (string, error) {
			var args weatherArgs
			if err := fullmodel.DecodeToolArguments(raw, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.City) == "" {
				args.City = "Hangzhou"
			}
			return fmt.Sprintf("%s 示例天气：晴，24°C，适合写代码。", args.City), nil
		},
	)
}

func runCapabilities(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		capabilities := client.Capabilities()
		if len(capabilities) == 0 {
			return fmt.Errorf("empty capabilities")
		}
		for _, capability := range capabilities {
			fmt.Printf("- %s streaming=%v\n", capability.Kind, capability.Streaming)
		}
		return nil
	}
}

func runTools(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		tools := client.Tools()
		if len(tools) == 0 {
			return fmt.Errorf("empty tools")
		}
		for _, tool := range tools {
			fmt.Printf("- %s\n", tool.Function.Name)
		}
		return nil
	}
}

func runText(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		text, err := client.Text(ctx, "用一句中文欢迎用户使用 FullModel SDK。", noDefaultTools())
		if err != nil {
			return err
		}
		fmt.Println(text)
		return requireText(text)
	}
}

func runRawRun(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		result, err := client.Run(ctx, processmessage.TextMessage{Text: "用三个词概括李白。"}, noDefaultTools())
		if err != nil {
			return err
		}
		if result == nil || result.Output == nil {
			return fmt.Errorf("empty result")
		}
		fmt.Println(result.Output.Content.Text)
		return requireText(result.Output.Content.Text)
	}
}

func runStreamText(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		stream, err := client.StreamText(ctx, "你好，介绍一下李白")
		if err != nil {
			return err
		}
		text, chunks, err := collectStream(stream)
		if err != nil {
			return err
		}
		fmt.Printf("\nchunks=%d\n", chunks)
		return requireText(text)
	}
}

func runTextStreamAlias(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		stream, err := client.TextStream(ctx, "用一句话说明诗仙是谁。")
		if err != nil {
			return err
		}
		text, chunks, err := collectStream(stream)
		if err != nil {
			return err
		}
		fmt.Printf("\nchunks=%d\n", chunks)
		return requireText(text)
	}
}

func runChatMemory(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		sessionID := fmt.Sprintf("sdk-example-%d", time.Now().UnixNano())
		defer client.ClearSession(sessionID)

		first, err := client.Chat(ctx, sessionID, "我叫 SDKExample。只回复：记住了。", noDefaultTools())
		if err != nil {
			return err
		}
		fmt.Println("first:", first)

		second, err := client.Chat(ctx, sessionID, "我叫什么？只回答名字。", noDefaultTools())
		if err != nil {
			return err
		}
		fmt.Println("second:", second)
		if !strings.Contains(second, "SDKExample") {
			return fmt.Errorf("expected remembered name, got %q", second)
		}
		return nil
	}
}

func runManualMemory(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		sessionID := "sdk-example-manual-memory"
		memory := client.Memory()
		memory.Clear(sessionID)
		memory.RememberSystem(sessionID, "你是一个极简助手。")
		memory.RememberUser(sessionID, "我喜欢 Go。")
		memory.RememberAssistant(sessionID, "记住了。")

		messages := memory.Messages(sessionID)
		fmt.Printf("messages=%d\n", len(messages))
		if len(messages) != 3 {
			return fmt.Errorf("expected 3 memory messages, got %d", len(messages))
		}
		client.ClearSession(sessionID)
		return nil
	}
}

func runToolLoop(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		answer, err := client.Text(ctx, "请调用 example_weather 工具查询 Hangzhou 的示例天气，然后用一句话回答。")
		if err != nil {
			return err
		}
		fmt.Println(answer)
		if !strings.Contains(answer, "Hangzhou") && !strings.Contains(answer, "杭州") {
			return fmt.Errorf("expected tool answer to mention Hangzhou, got %q", answer)
		}
		return nil
	}
}

func runExecuteTool(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		result, err := client.ExecuteTool(ctx, brain.ToolCall{
			Function: brain.FunctionCall{
				Name:      "example_weather",
				Arguments: `{"city":"Hangzhou"}`,
			},
		})
		if err != nil {
			return err
		}
		fmt.Println(result)
		if !strings.Contains(result, "Hangzhou") {
			return fmt.Errorf("unexpected tool result: %q", result)
		}
		return nil
	}
}

func runTTS(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		audio, err := client.TTS(ctx, "你好，欢迎使用 FullModel SDK。")
		if err != nil {
			return err
		}
		fmt.Printf("audio_bytes=%d\n", len(audio))
		if len(audio) == 0 {
			return fmt.Errorf("empty audio")
		}
		return nil
	}
}

func runGenerateImage(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		url, err := client.GenerateImage(ctx, "一张极简风格的 FullModel SDK 欢迎卡片")
		if err != nil {
			return err
		}
		fmt.Println(url)
		return requireText(url)
	}
}

func runTextToVideo(client *fullmodel.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		url, err := client.TextToVideo(ctx, "一个极简机器人在终端里输出 hello")
		if err != nil {
			return err
		}
		fmt.Println(url)
		return requireText(url)
	}
}

func collectStream(stream brain.StreamOutput) (string, int, error) {
	if stream == nil {
		return "", 0, fmt.Errorf("nil stream")
	}
	var text strings.Builder
	chunks := 0
	for chunk := range stream.Text() {
		chunks++
		fmt.Print(chunk)
		text.WriteString(chunk)
	}
	if err := stream.Wait(); err != nil {
		return text.String(), chunks, err
	}
	if chunks == 0 {
		return text.String(), chunks, fmt.Errorf("stream returned zero chunks")
	}
	return text.String(), chunks, nil
}

func noDefaultTools() fullmodel.RunOption {
	return fullmodel.WithRuntimeTools(false)
}

func requireText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("empty text")
	}
	return nil
}

func fatal(name string, err error) {
	fmt.Fprintf(os.Stderr, "[FAIL] %s: %v\n", name, err)
	os.Exit(1)
}
