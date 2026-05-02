package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xumi30/fullmodel/agent/brain"
	agentruntime "github.com/xumi30/fullmodel/agent/runtime"
	"github.com/xumi30/fullmodel/processmessage"
	"github.com/xumi30/fullmodel/utils/fileop"
)

type brainSet struct {
	runner      *agentruntime.Runner
	system      string
	chatHistory []brain.Message
}

func main() {
	configFile := flag.String("config", "", "config file path, default config/llm.yaml")
	systemPrompt := flag.String("system", "", "system prompt for text/chat commands")
	stream := flag.Bool("stream", false, "stream text/chat responses by default")
	flag.Parse()

	app, err := newBrainSet(*configFile, *systemPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("fullmodel cli")
	fmt.Println("type help for commands, quit to exit")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 16*1024), 4*1024*1024)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "quit" || line == "exit" {
			return
		}

		if err := app.handle(context.Background(), line, *stream); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "read failed: %v\n", err)
	}
}

func newBrainSet(configFile, systemPrompt string) (*brainSet, error) {
	cfgs, err := loadConfigs(configFile)
	if err != nil {
		return nil, err
	}

	registry, err := agentruntime.NewRegistryFromConfigs(cfgs)
	if err != nil {
		return nil, err
	}
	return &brainSet{
		runner: agentruntime.NewRunner(registry, nil),
		system: systemPrompt,
	}, nil
}

func loadConfigs(configFile string) (*fileop.BrainConfigs, error) {
	if strings.TrimSpace(configFile) != "" {
		return fileop.LoadBrainConfigsFromFile(configFile)
	}
	return fileop.LoadBrainConfigs()
}

func (app *brainSet) handle(ctx context.Context, line string, streamByDefault bool) error {
	cmd, arg := splitCommand(line)
	switch cmd {
	case "help", "?":
		printHelp()
		return nil
	case "clear":
		app.chatHistory = nil
		fmt.Println("chat history cleared")
		return nil
	case "text":
		return app.runText(ctx, arg, false)
	case "stream":
		return app.runText(ctx, arg, true)
	case "chat":
		return app.runChat(ctx, arg, streamByDefault)
	case "image":
		path, prompt, err := splitTargetPrompt(arg)
		if err != nil {
			return err
		}
		media, err := loadMedia(path)
		if err != nil {
			return err
		}
		return app.run(ctx, processmessage.ImageMessage{Prompt: prompt, Image: media}, processmessage.Options{})
	case "video":
		path, prompt, err := splitTargetPrompt(arg)
		if err != nil {
			return err
		}
		media, err := loadMedia(path)
		if err != nil {
			return err
		}
		return app.run(ctx, processmessage.VideoMessage{Prompt: prompt, Video: media}, processmessage.Options{})
	case "asr":
		if arg == "" {
			return errors.New("usage: asr <audio-file>")
		}
		media, err := loadMedia(arg)
		if err != nil {
			return err
		}
		return app.run(ctx, processmessage.SpeechToTextMessage{Audio: media}, processmessage.Options{})
	case "tts":
		text, out := splitOptionalOut(arg)
		if strings.TrimSpace(text) == "" {
			return errors.New("usage: tts <text> [> output.mp3]")
		}
		return app.runWithOutput(ctx, processmessage.TextToSpeechMessage{Text: text}, processmessage.Options{}, out)
	case "imagine":
		if arg == "" {
			return errors.New("usage: imagine <prompt>")
		}
		return app.run(ctx, processmessage.ImageGenerateMessage{Prompt: arg}, processmessage.Options{})
	case "edit":
		path, prompt, err := splitTargetPrompt(arg)
		if err != nil {
			return err
		}
		media, err := loadMedia(path)
		if err != nil {
			return err
		}
		return app.run(ctx, processmessage.ImageEditMessage{
			Prompt: prompt,
			Images: []brain.MediaResource{media},
		}, processmessage.Options{})
	case "t2v":
		if arg == "" {
			return errors.New("usage: t2v <prompt>")
		}
		return app.run(ctx, processmessage.TextToVideoMessage{Prompt: arg}, processmessage.Options{})
	case "i2v":
		path, prompt, err := splitTargetPrompt(arg)
		if err != nil {
			return err
		}
		media, err := loadMedia(path)
		if err != nil {
			return err
		}
		return app.run(ctx, processmessage.ImageToVideoMessage{Prompt: prompt, FirstFrame: media}, processmessage.Options{})
	default:
		return fmt.Errorf("unknown command %q, type help", cmd)
	}
}

func (app *brainSet) runText(ctx context.Context, text string, stream bool) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("usage: text <prompt>")
	}
	return app.run(ctx, processmessage.TextMessage{
		System: app.system,
		Text:   text,
	}, processmessage.Options{Context: ctx, Stream: stream})
}

func (app *brainSet) runChat(ctx context.Context, text string, stream bool) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("usage: chat <message>")
	}

	msg := processmessage.TextMessage{
		System:  app.system,
		History: app.chatHistory,
		Text:    text,
	}
	out, err := app.runner.Run(ctx, agentruntime.Request{
		Message: msg,
		Options: processmessage.Options{Context: ctx, Stream: stream},
	})
	if err != nil {
		return err
	}
	if err := printOutput(out.Output, ""); err != nil {
		return err
	}

	app.chatHistory = append(app.chatHistory, brain.NewUserMessage(text))
	if assistant := strings.TrimSpace(out.Output.Content.Text); assistant != "" {
		app.chatHistory = append(app.chatHistory, brain.NewAssistantMessage(assistant))
	}
	return nil
}

func (app *brainSet) run(ctx context.Context, msg processmessage.Message, opts processmessage.Options) error {
	return app.runWithOutput(ctx, msg, opts, "")
}

func (app *brainSet) runWithOutput(ctx context.Context, msg processmessage.Message, opts processmessage.Options, outPath string) error {
	opts.Context = ctx
	out, err := app.runner.Run(ctx, agentruntime.Request{Message: msg, Options: opts})
	if err != nil {
		return err
	}
	return printOutput(out.Output, outPath)
}

func printOutput(out *brain.BrainOutput, outPath string) error {
	if out == nil {
		return errors.New("empty brain output")
	}
	if !out.Status.Success {
		return fmt.Errorf("brain failed: %s", out.Status.Error)
	}
	if out.Stream != nil {
		return printStream(out.Stream)
	}

	if text := strings.TrimSpace(out.Content.Text); text != "" {
		fmt.Println(text)
	}
	if url := strings.TrimSpace(out.Content.Image.URL); url != "" {
		fmt.Println("image:", url)
	}
	if url := strings.TrimSpace(out.Content.Video.URL); url != "" {
		fmt.Println("video:", url)
	}
	if len(out.Content.Audio.Data) > 0 {
		path := outPath
		if path == "" {
			path = fmt.Sprintf("tts_%s.mp3", time.Now().Format("20060102_150405"))
		}
		if err := os.WriteFile(path, out.Content.Audio.Data, 0o644); err != nil {
			return err
		}
		fmt.Println("audio:", path)
	}
	if len(out.Metadata) > 0 {
		fmt.Printf("metadata: %+v\n", out.Metadata)
	}
	return nil
}

func printStream(stream brain.StreamOutput) error {
	for {
		select {
		case text, ok := <-stream.Text():
			if !ok {
				if err := stream.Wait(); err != nil {
					return err
				}
				fmt.Println()
				return nil
			}
			fmt.Print(text)
		case toolCalls, ok := <-stream.ToolCalls():
			if ok && len(toolCalls) > 0 {
				fmt.Printf("\n[tool_calls] %+v\n", toolCalls)
			}
		case err, ok := <-stream.Error():
			if ok && err != nil {
				return err
			}
		}
	}
}

func splitCommand(line string) (string, string) {
	cmd, arg, ok := strings.Cut(strings.TrimSpace(line), " ")
	if !ok {
		return strings.TrimSpace(line), ""
	}
	return strings.TrimSpace(cmd), strings.TrimSpace(arg)
}

func splitTargetPrompt(arg string) (string, string, error) {
	target, prompt, ok := strings.Cut(arg, "|")
	if !ok {
		fields := strings.Fields(arg)
		if len(fields) == 0 {
			return "", "", errors.New("usage: <command> <file-or-url> | <prompt>")
		}
		return fields[0], strings.TrimSpace(strings.TrimPrefix(arg, fields[0])), nil
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", errors.New("missing file or url")
	}
	return target, strings.TrimSpace(prompt), nil
}

func splitOptionalOut(arg string) (string, string) {
	text, out, ok := strings.Cut(arg, ">")
	if !ok {
		return strings.TrimSpace(arg), ""
	}
	return strings.TrimSpace(text), strings.TrimSpace(out)
}

func loadMedia(target string) (brain.MediaResource, error) {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "data:") {
		return brain.MediaResource{URL: target}, nil
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return brain.MediaResource{}, fmt.Errorf("read media %s: %w", target, err)
	}
	return brain.MediaResource{
		Data:     data,
		MimeType: detectMime(target, data),
	}, nil
}

func detectMime(path string, data []byte) string {
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

func printHelp() {
	_, _ = io.WriteString(os.Stdout, `commands:
  text <prompt>                     one-shot text
  stream <prompt>                   streaming text
  chat <message>                    multi-turn chat, keeps history
  clear                             clear chat history
  image <file-or-url> | <prompt>    image understanding
  video <file-or-url> | <prompt>    video understanding
  asr <audio-file>                  speech to text
  tts <text> [> output.mp3]         text to speech
  imagine <prompt>                  text to image
  edit <image> | <prompt>           image edit
  t2v <prompt>                      text to video
  i2v <image> | <prompt>            image to video
  quit                              exit

examples:
  chat 你好，介绍一下你自己
  image ./cat.png | 这张图里有什么？
  tts 你好，我是 fullmodel > hello.mp3
`)
}
