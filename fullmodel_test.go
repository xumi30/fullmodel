package fullmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xumi30/fullmodel/agent/brain"
	agentruntime "github.com/xumi30/fullmodel/agent/runtime"
	"github.com/xumi30/fullmodel/processmessage"
	"github.com/xumi30/fullmodel/utils/fileop"

	"github.com/stretchr/testify/require"
)

func TestOpenWithConfigs(t *testing.T) {
	client, err := Open(WithConfigs(&fileop.BrainConfigs{
		Defaults: fileop.ModelConfig{
			Provider: "qwen",
			Region:   "cn-beijing",
			APIKey:   "test",
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotEmpty(t, client.Capabilities())
	require.NotEmpty(t, client.Tools())
	var hasBaidu bool
	for _, tool := range client.Tools() {
		if tool.Function.Name == "baidu_search" {
			hasBaidu = true
			break
		}
	}
	require.True(t, hasBaidu)
}

func TestRunNilClient(t *testing.T) {
	var client *Client
	_, err := client.Run(context.Background(), processmessage.TextMessage{Text: "hello"})
	require.Error(t, err)
}

func TestMediaFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.png")
	require.NoError(t, os.WriteFile(path, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, 0o644))

	media, err := MediaFromFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, media.Data)
	require.Equal(t, "image/png", media.MimeType)
}

func TestHighLevelHelpersUseRun(t *testing.T) {
	client := &Client{
		runner: fakeRunner(t, processmessage.KindText, &brain.BrainOutput{
			Status: brain.BrainStatus{Success: true},
			Content: brain.BrainOutputContent{
				Text: "hello",
			},
		}),
	}

	text, err := client.Text(context.Background(), "hi")
	require.NoError(t, err)
	require.Equal(t, "hello", text)
}

func TestStreamTextReturnsStream(t *testing.T) {
	client := &Client{
		runner: fakeRunner(t, processmessage.KindText, &brain.BrainOutput{
			Status: brain.BrainStatus{Success: true},
			Stream: newFakeStream(
				[]string{"你", "好"},
				nil,
			),
		}),
	}

	stream, err := client.StreamText(context.Background(), "写一句欢迎语")
	require.NoError(t, err)
	require.NotNil(t, stream)

	first, ok := <-stream.Text()
	require.True(t, ok)
	require.Equal(t, "你", first)
}

func TestStreamTextSDKReadsSSEChunks(t *testing.T) {
	var sawStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)

		var body struct {
			Stream bool  `json:"stream"`
			Tools  []any `json:"tools"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		sawStream = body.Stream
		require.Empty(t, body.Tools)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		for _, chunk := range []string{"你好，", "李白是唐代诗人。"} {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", chunk)
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client, err := Open(WithConfigs(&fileop.BrainConfigs{
		Defaults: fileop.ModelConfig{
			Provider: "generic",
			APIKey:   "test",
			BaseURL:  server.URL,
			Model:    "test-model",
		},
	}))
	require.NoError(t, err)

	stream, err := client.StreamText(context.Background(), "你好，介绍一下李白")
	require.NoError(t, err)
	require.NotNil(t, stream)

	var text strings.Builder
	for chunk := range stream.Text() {
		text.WriteString(chunk)
	}
	require.NoError(t, stream.Wait())
	require.True(t, sawStream)
	require.Equal(t, "你好，李白是唐代诗人。", text.String())
}

func TestStreamTextWithSessionRemembersUserOnly(t *testing.T) {
	client := &Client{
		runner:   fakeRunner(t, processmessage.KindText, streamOutput("你", "好")),
		sessions: agentruntime.NewSessionStore(),
	}

	stream, err := client.StreamText(context.Background(), "hello", WithSession("s1"))
	require.NoError(t, err)
	for range stream.Text() {
	}
	require.NoError(t, stream.Wait())

	messages := client.Memory().Messages("s1")
	require.Len(t, messages, 1)
	require.Equal(t, "user", messages[0].Role)
	require.Equal(t, "hello", messages[0].Content)
}

func TestStreamChatRemembersAssistantAfterWait(t *testing.T) {
	client := &Client{
		runner:   fakeRunner(t, processmessage.KindText, streamOutput("你", "好")),
		sessions: agentruntime.NewSessionStore(),
	}

	stream, err := client.StreamChat(context.Background(), "s1", "hello")
	require.NoError(t, err)

	var text strings.Builder
	for chunk := range stream.Text() {
		text.WriteString(chunk)
	}
	require.Equal(t, "你好", text.String())
	require.NoError(t, stream.Wait())

	messages := client.Memory().Messages("s1")
	require.Len(t, messages, 2)
	require.Equal(t, "user", messages[0].Role)
	require.Equal(t, "hello", messages[0].Content)
	require.Equal(t, "assistant", messages[1].Role)
	require.Equal(t, "你好", messages[1].Content)
}

func TestMemoryManager(t *testing.T) {
	client := &Client{sessions: agentruntime.NewSessionStore()}
	memory := client.Memory()

	memory.RememberSystem("s1", "system")
	memory.RememberUser("s1", "hello")
	memory.RememberAssistant("s1", "hi")

	messages := memory.Messages("s1")
	require.Len(t, messages, 3)
	require.Equal(t, "system", messages[0].Role)
	require.Equal(t, "user", messages[1].Role)
	require.Equal(t, "assistant", messages[2].Role)

	memory.Replace("s1", []brain.Message{brain.NewUserMessage("reset")})
	messages = memory.Messages("s1")
	require.Len(t, messages, 1)
	require.Equal(t, "reset", messages[0].Content)

	client.ClearSession("s1")
	require.Empty(t, memory.Messages("s1"))
}

func TestAttachSessionMergesChatHistory(t *testing.T) {
	store := agentruntime.NewSessionStore()
	store.Append("s1", brain.NewUserMessage("stored"))
	client := &Client{sessions: store}

	msg := client.attachSession(processmessage.ChatMessage{
		Messages: []brain.Message{brain.NewUserMessage("inline")},
	}, "s1")

	chat, ok := msg.(processmessage.ChatMessage)
	require.True(t, ok)
	require.Len(t, chat.Messages, 2)
	require.Equal(t, "stored", chat.Messages[0].Content)
	require.Equal(t, "inline", chat.Messages[1].Content)
}

func TestToolSet(t *testing.T) {
	type args struct {
		City string `json:"city"`
	}
	toolSet := NewToolSet(NewTool(
		"weather",
		"Get weather by city",
		ObjectSchema(map[string]any{
			"city": map[string]any{"type": "string"},
		}, "city"),
		func(ctx context.Context, raw string) (string, error) {
			var input args
			require.NoError(t, DecodeToolArguments(raw, &input))
			return "sunny in " + input.City, nil
		},
	))

	tools := toolSet.Tools()
	require.Len(t, tools, 1)
	require.Equal(t, "weather", tools[0].Function.Name)

	result, err := toolSet.Execute(context.Background(), brain.ToolCall{
		Function: brain.FunctionCall{
			Name:      "weather",
			Arguments: `{"city":"Hangzhou"}`,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "sunny in Hangzhou", result)
}

func TestClientToolsUsesConfiguredExecutor(t *testing.T) {
	toolSet := NewToolSet(NewTool("ping", "Ping tool", ObjectSchema(nil), func(ctx context.Context, arguments string) (string, error) {
		return "pong", nil
	}))
	client := &Client{tools: toolSet}

	tools := client.Tools()
	require.Len(t, tools, 1)
	require.Equal(t, "ping", tools[0].Function.Name)

	result, err := client.ExecuteTool(context.Background(), brain.ToolCall{
		Function: brain.FunctionCall{Name: "ping"},
	})
	require.NoError(t, err)
	require.Equal(t, "pong", result)
}

func TestTTSRunOptions(t *testing.T) {
	runOpts := runOptions{}
	WithTTSVoice("longxiaochun")(&runOpts)
	WithTTSFormat("wav")(&runOpts)
	WithTTSSampleRate(48000)(&runOpts)
	WithTTSVolume(80)(&runOpts)
	WithTTSRate(1.2)(&runOpts)
	WithTTSPitch(0.9)(&runOpts)
	WithTTSSSML(true)(&runOpts)

	require.Equal(t, "longxiaochun", runOpts.options.Extra["voice"])
	require.Equal(t, "wav", runOpts.options.Extra["format"])
	require.Equal(t, 48000, runOpts.options.Extra["sample_rate"])
	require.Equal(t, 80, runOpts.options.Extra["volume"])
	require.Equal(t, 1.2, runOpts.options.Extra["rate"])
	require.Equal(t, 0.9, runOpts.options.Extra["pitch"])
	require.Equal(t, true, runOpts.options.Extra["enable_ssml"])
}

func streamOutput(chunks ...string) *brain.BrainOutput {
	return &brain.BrainOutput{
		Status: brain.BrainStatus{Success: true},
		Stream: newFakeStream(
			chunks,
			nil,
		),
	}
}

func fakeRunner(t *testing.T, kind processmessage.Kind, output *brain.BrainOutput) *agentruntime.Runner {
	t.Helper()
	registry := agentruntime.NewRegistry()
	require.NoError(t, registry.Register(kind, fakeSDKBrain{output: output}, agentruntime.Capability{}))
	return agentruntime.NewRunner(registry, nil)
}

type fakeSDKBrain struct {
	output *brain.BrainOutput
}

func (f fakeSDKBrain) ProcessInput(input *brain.BrainInput) (*brain.BrainOutput, error) {
	return f.output, nil
}

type fakeStream struct {
	textCh chan string
	errCh  chan error
	toolCh chan []brain.ToolCall
	err    error
}

func newFakeStream(chunks []string, err error) *fakeStream {
	textCh := make(chan string, len(chunks))
	for _, chunk := range chunks {
		textCh <- chunk
	}
	close(textCh)
	errCh := make(chan error, 1)
	if err != nil {
		errCh <- err
	}
	close(errCh)
	toolCh := make(chan []brain.ToolCall)
	close(toolCh)
	return &fakeStream{textCh: textCh, errCh: errCh, toolCh: toolCh, err: err}
}

func (s *fakeStream) Text() <-chan string {
	return s.textCh
}

func (s *fakeStream) Error() <-chan error {
	return s.errCh
}

func (s *fakeStream) ToolCalls() <-chan []brain.ToolCall {
	return s.toolCh
}

func (s *fakeStream) Cancel() {}

func (s *fakeStream) Wait() error {
	return s.err
}
