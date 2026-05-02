package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/processmessage"

	"github.com/stretchr/testify/require"
)

const (
	testEventuallyTimeout = time.Second
	testEventuallyTick    = 10 * time.Millisecond
)

type fakeBrain struct {
	calls int
}

func (f *fakeBrain) ProcessInput(input *brain.BrainInput) (*brain.BrainOutput, error) {
	f.calls++
	if f.calls == 1 {
		return &brain.BrainOutput{
			Mode:   brain.BrainModeText,
			Status: brain.BrainStatus{Success: true},
			Content: brain.BrainOutputContent{
				Messages: []brain.Message{
					{
						Role:    "assistant",
						Content: "",
						ToolCalls: []brain.ToolCall{
							{
								ID:   "call_1",
								Type: "function",
								Function: brain.FunctionCall{
									Name:      "now",
									Arguments: "{}",
								},
							},
						},
					},
				},
			},
		}, nil
	}

	return &brain.BrainOutput{
		Mode:   brain.BrainModeText,
		Status: brain.BrainStatus{Success: true},
		Content: brain.BrainOutputContent{
			Text: "done",
			Messages: []brain.Message{
				brain.NewAssistantMessage("done"),
			},
		},
	}, nil
}

type fakeToolExecutor struct{}

func (fakeToolExecutor) Tools() []brain.Tool {
	return []brain.Tool{
		{
			Type: "function",
			Function: brain.FunctionDefinition{
				Name:        "now",
				Description: "returns time",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}
}

func (fakeToolExecutor) Execute(ctx context.Context, call brain.ToolCall) (string, error) {
	return `{"time":"now"}`, nil
}

type cancelAwareBrain struct {
	started chan struct{}
}

func (b cancelAwareBrain) ProcessInput(input *brain.BrainInput) (*brain.BrainOutput, error) {
	close(b.started)
	<-input.ContextOrBackground().Done()
	return nil, input.ContextOrBackground().Err()
}

func TestRunnerRunsToolLoop(t *testing.T) {
	registry := NewRegistry()
	brainImpl := &fakeBrain{}
	require.NoError(t, registry.Register(processmessage.KindText, brainImpl, Capability{}))
	require.NoError(t, registry.Register(processmessage.KindChat, brainImpl, Capability{}))

	runner := NewRunner(registry, fakeToolExecutor{})
	result, err := runner.Run(context.Background(), Request{
		Message: processmessage.TextMessage{Text: "what time is it"},
	})

	require.NoError(t, err)
	require.Equal(t, "done", result.Output.Content.Text)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, 2, brainImpl.calls)
	require.Len(t, result.Messages, 4)
	require.Equal(t, "tool", result.Messages[2].Role)
}

func TestTaskStoreRunsRequest(t *testing.T) {
	registry := NewRegistry()
	brainImpl := &fakeBrain{calls: 1}
	require.NoError(t, registry.Register(processmessage.KindText, brainImpl, Capability{}))

	runner := NewRunner(registry, nil)
	store := NewTaskStore()
	task, err := store.Start(context.Background(), runner, Request{
		Message: processmessage.TextMessage{Text: "hello"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, task.ID)

	require.Eventually(t, func() bool {
		got, ok := store.Get(task.ID)
		return ok && got.Status == TaskSucceeded && got.Result != nil
	}, testEventuallyTimeout, testEventuallyTick)
}

func TestTaskStoreCancel(t *testing.T) {
	registry := NewRegistry()
	started := make(chan struct{})
	require.NoError(t, registry.Register(processmessage.KindText, cancelAwareBrain{started: started}, Capability{}))

	runner := NewRunner(registry, nil)
	store := NewTaskStore(1)
	task, err := store.StartWithOptions(context.Background(), runner, Request{
		Message: processmessage.TextMessage{Text: "hello"},
	}, TaskOptions{Kind: string(processmessage.KindText), SessionID: "s1"})
	require.NoError(t, err)

	<-started
	canceled, ok := store.Cancel(task.ID)
	require.True(t, ok)
	require.Equal(t, TaskCanceled, canceled.Status)

	require.Eventually(t, func() bool {
		got, ok := store.Get(task.ID)
		return ok && got.Status == TaskCanceled && got.SessionID == "s1"
	}, testEventuallyTimeout, testEventuallyTick)
}
