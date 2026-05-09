package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xumi30/fullmodel"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), envDuration("FULLMODEL_EXAMPLE_TIMEOUT", 2*time.Minute))
	defer cancel()

	client, err := fullmodel.Open()
	if err != nil {
		fatal("open client", err)
	}

	switch os.Args[1] {
	case "list-voices":
		err = listVoices(ctx, client)
	case "clone-voice":
		err = cloneVoice(ctx, client)
	case "delete-voice":
		err = deleteVoice(ctx, client)
	case "realtime-tts":
		err = realtimeTTS(ctx, client)
	case "realtime-dialog":
		err = realtimeDialog(ctx, client)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fatal(os.Args[1], err)
	}
}

func listVoices(ctx context.Context, client *fullmodel.Client) error {
	result, err := client.ListVoices(ctx, fullmodel.VoiceListRequest{
		PageSize:  envInt("FULLMODEL_VOICE_PAGE_SIZE", 20),
		PageIndex: envInt("FULLMODEL_VOICE_PAGE_INDEX", 1),
	})
	if err != nil {
		return err
	}
	printJSON(result)
	return nil
}

func cloneVoice(ctx context.Context, client *fullmodel.Client) error {
	samplePath := mustEnv("FULLMODEL_VOICE_SAMPLE")
	sample, err := fullmodel.MediaFromFile(samplePath)
	if err != nil {
		return err
	}

	result, err := client.CloneVoice(ctx, fullmodel.VoiceCloneRequest{
		Audio:         sample,
		PreferredName: env("FULLMODEL_VOICE_NAME", "fullmodel_voice"),
		TargetModel:   env("FULLMODEL_VOICE_TARGET_MODEL", fullmodel.QwenTTSVCRealtimeModel),
		Language:      env("FULLMODEL_VOICE_LANGUAGE", "zh"),
		Text:          os.Getenv("FULLMODEL_VOICE_TEXT"),
	})
	if err != nil {
		return err
	}
	printJSON(result)
	return nil
}

func deleteVoice(ctx context.Context, client *fullmodel.Client) error {
	requestID, err := client.DeleteVoice(ctx, mustEnv("FULLMODEL_VOICE_ID"))
	if err != nil {
		return err
	}
	fmt.Printf("deleted voice, request_id=%s\n", requestID)
	return nil
}

func realtimeTTS(ctx context.Context, client *fullmodel.Client) error {
	session, err := client.RealtimeTTS(ctx, fullmodel.RealtimeTTSConfig{
		Model:          env("FULLMODEL_REALTIME_TTS_MODEL", fullmodel.QwenTTSFlashRealtimeModel),
		Voice:          env("FULLMODEL_REALTIME_TTS_VOICE", "Cherry"),
		Mode:           env("FULLMODEL_REALTIME_TTS_MODE", fullmodel.QwenRealtimeModeServerCommit),
		LanguageType:   env("FULLMODEL_REALTIME_TTS_LANGUAGE_TYPE", "Chinese"),
		ResponseFormat: env("FULLMODEL_REALTIME_TTS_FORMAT", "pcm"),
		SampleRate:     envInt("FULLMODEL_REALTIME_TTS_SAMPLE_RATE", 24000),
		Instructions:   os.Getenv("FULLMODEL_REALTIME_TTS_INSTRUCTIONS"),
	})
	if err != nil {
		return err
	}
	defer session.Close()

	go func() {
		for event := range session.Events() {
			if event.Type != "response.audio.delta" {
				fmt.Printf("event=%s\n", event.Type)
			}
		}
	}()

	if err := session.AppendText(env("FULLMODEL_REALTIME_TTS_TEXT", "你好，欢迎使用 FullModel 实时语音合成。")); err != nil {
		return err
	}
	if os.Getenv("FULLMODEL_REALTIME_TTS_MODE") == fullmodel.QwenRealtimeModeCommit {
		if err := session.Commit(); err != nil {
			return err
		}
	}
	if err := session.Finish(); err != nil {
		return err
	}

	audio, err := session.CollectAudio(ctx)
	if err != nil {
		return err
	}
	if len(audio) == 0 {
		return fmt.Errorf("empty realtime tts audio")
	}
	out := env("FULLMODEL_REALTIME_TTS_OUTPUT", "realtime_tts.pcm")
	if err := os.WriteFile(out, audio, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d bytes to %s\n", len(audio), out)
	return nil
}

func realtimeDialog(ctx context.Context, client *fullmodel.Client) error {
	session, err := client.RealtimeDialog(ctx, fullmodel.RealtimeDialogConfig{
		WorkspaceID: mustEnv("FULLMODEL_DIALOG_WORKSPACE_ID"),
		AppID:       mustEnv("FULLMODEL_DIALOG_APP_ID"),
		DialogID:    os.Getenv("FULLMODEL_DIALOG_ID"),
		Upstream: map[string]any{
			"type":         env("FULLMODEL_DIALOG_UPSTREAM_TYPE", "AudioOnly"),
			"mode":         env("FULLMODEL_DIALOG_UPSTREAM_MODE", "push2talk"),
			"audio_format": env("FULLMODEL_DIALOG_AUDIO_FORMAT", "pcm"),
			"sample_rate":  envInt("FULLMODEL_DIALOG_SAMPLE_RATE", 16000),
		},
		Downstream: map[string]any{
			"audio_format": env("FULLMODEL_DIALOG_DOWNSTREAM_AUDIO_FORMAT", "pcm"),
			"sample_rate":  envInt("FULLMODEL_DIALOG_DOWNSTREAM_SAMPLE_RATE", 24000),
		},
	})
	if err != nil {
		return err
	}
	defer session.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case event, ok := <-session.Events():
				if !ok {
					return
				}
				printJSON(event)
			case audio, ok := <-session.Audio():
				if !ok {
					return
				}
				fmt.Printf("downstream_audio_bytes=%d\n", len(audio))
			case err := <-session.Errors():
				if err != nil {
					fmt.Fprintf(os.Stderr, "dialog error: %v\n", err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	if audioPath := strings.TrimSpace(os.Getenv("FULLMODEL_DIALOG_AUDIO")); audioPath != "" {
		audio, err := os.ReadFile(audioPath)
		if err != nil {
			return err
		}
		if err := session.SendSpeech(); err != nil {
			return err
		}
		for off := 0; off < len(audio); off += 3200 {
			end := off + 3200
			if end > len(audio) {
				end = len(audio)
			}
			if err := session.SendAudio(audio[off:end]); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := session.StopSpeech(); err != nil {
			return err
		}
	} else {
		if err := session.RequestToRespond(
			fullmodel.RealtimeDialogRespondPrompt,
			env("FULLMODEL_DIALOG_PROMPT", "请用一句中文介绍 FullModel。"),
			nil,
		); err != nil {
			return err
		}
	}

	select {
	case <-done:
	case <-time.After(envDuration("FULLMODEL_DIALOG_LISTEN", 20*time.Second)):
	}
	return session.Finish()
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  go run ./examples/aliyun_voice_realtime list-voices
  FULLMODEL_VOICE_SAMPLE=./voice.mp3 go run ./examples/aliyun_voice_realtime clone-voice
  FULLMODEL_VOICE_ID=<voice> go run ./examples/aliyun_voice_realtime delete-voice
  FULLMODEL_REALTIME_TTS_VOICE=<voice-or-Cherry> go run ./examples/aliyun_voice_realtime realtime-tts
  FULLMODEL_DIALOG_WORKSPACE_ID=<workspace> FULLMODEL_DIALOG_APP_ID=<app> go run ./examples/aliyun_voice_realtime realtime-dialog

All commands perform real DashScope calls through fullmodel.Open(), so set config/llm.yaml or DASHSCOPE_API_KEY first.`)
}

func printJSON(value any) {
	data, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(data))
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value != "" {
		return value
	}
	return fallback
}

func mustEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		fatal("missing env", fmt.Errorf("%s is required", key))
	}
	return value
}

func envInt(key string, fallback int) int {
	var value int
	if _, err := fmt.Sscanf(strings.TrimSpace(os.Getenv(key)), "%d", &value); err == nil && value > 0 {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return d
}

func fatal(name string, err error) {
	fmt.Fprintf(os.Stderr, "[FAIL] %s: %v\n", name, err)
	os.Exit(1)
}
