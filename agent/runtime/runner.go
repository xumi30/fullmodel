package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/processmessage"
)

const defaultMaxToolRounds = 4

// Request is a normalized runtime request.
type Request struct {
	Message processmessage.Message
	Options processmessage.Options
}

// Result is the runtime response plus normalized execution trace.
type Result struct {
	Output    *brain.BrainOutput `json:"output"`
	Messages  []brain.Message    `json:"messages,omitempty"`
	ToolCalls []brain.ToolCall   `json:"tool_calls,omitempty"`
	Rounds    int                `json:"rounds"`
}

// Runner is the central execution engine shared by CLI and HTTP.
type Runner struct {
	registry      *Registry
	processor     processmessage.Processor
	toolExecutor  ToolExecutor
	maxToolRounds int
}

func NewRunner(registry *Registry, toolExecutor ToolExecutor) *Runner {
	return &Runner{
		registry:      registry,
		processor:     processmessage.NewProcessor(registry),
		toolExecutor:  toolExecutor,
		maxToolRounds: defaultMaxToolRounds,
	}
}

func (r *Runner) Registry() *Registry {
	if r == nil {
		return nil
	}
	return r.registry
}

func (r *Runner) Run(ctx context.Context, request Request) (*Result, error) {
	if r == nil || r.processor == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	if request.Options.Context == nil {
		request.Options.Context = ctx
	}
	if request.Options.Context == nil {
		request.Options.Context = context.Background()
	}
	if len(request.Options.Tools) == 0 && r.toolExecutor != nil {
		request.Options.Tools = r.toolExecutor.Tools()
	}

	result, err := r.runOnce(request)
	if err != nil {
		return nil, err
	}
	if !shouldRunToolLoop(request, result.Output, r.toolExecutor) {
		return result, nil
	}
	return r.runToolLoop(request, result)
}

func (r *Runner) runOnce(request Request) (*Result, error) {
	input, err := r.processor.BuildInput(request.Message, request.Options)
	if err != nil {
		return nil, err
	}
	processor, ok := r.registry.SelectBrain(request.Message.MessageKind())
	if !ok || processor == nil {
		return nil, fmt.Errorf("no brain registered for message kind %q", request.Message.MessageKind())
	}
	output, err := processor.ProcessInput(input)
	if err != nil {
		return nil, err
	}
	return &Result{
		Output:   output,
		Messages: append([]brain.Message(nil), input.Messages...),
		Rounds:   1,
	}, nil
}

func (r *Runner) runToolLoop(request Request, first *Result) (*Result, error) {
	messages := append([]brain.Message(nil), first.Messages...)
	output := first.Output
	allToolCalls := make([]brain.ToolCall, 0)
	maxRounds := r.maxToolRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxToolRounds
	}

	for round := 0; round < maxRounds; round++ {
		assistantMessages := assistantMessagesFromOutput(output)
		if len(assistantMessages) == 0 {
			break
		}
		messages = append(messages, assistantMessages...)

		toolCalls := toolCallsFromMessages(assistantMessages)
		if len(toolCalls) == 0 {
			break
		}
		allToolCalls = append(allToolCalls, toolCalls...)

		for _, call := range toolCalls {
			content, err := r.toolExecutor.Execute(request.Options.Context, call)
			if err != nil {
				content = toolErrorPayload(err)
			}
			messages = append(messages, brain.NewToolMessage(call.ID, content))
		}

		nextOptions := request.Options
		nextOptions.Stream = false
		nextRequest := Request{
			Message: processmessage.ChatMessage{Messages: messages},
			Options: nextOptions,
		}
		next, err := r.runOnce(nextRequest)
		if err != nil {
			return nil, err
		}
		output = next.Output
	}

	return &Result{
		Output:    output,
		Messages:  messages,
		ToolCalls: allToolCalls,
		Rounds:    1 + len(allToolCalls),
	}, nil
}

func shouldRunToolLoop(request Request, output *brain.BrainOutput, executor ToolExecutor) bool {
	if executor == nil || output == nil || output.Stream != nil || request.Options.Stream {
		return false
	}
	switch request.Message.MessageKind() {
	case processmessage.KindText, processmessage.KindChat:
	default:
		return false
	}
	return len(toolCallsFromMessages(assistantMessagesFromOutput(output))) > 0
}

func assistantMessagesFromOutput(output *brain.BrainOutput) []brain.Message {
	if output == nil {
		return nil
	}
	if len(output.Content.Messages) > 0 {
		return output.Content.Messages
	}
	if strings.TrimSpace(output.Content.Text) != "" {
		return []brain.Message{brain.NewAssistantMessage(output.Content.Text)}
	}
	if len(output.Choices) > 0 {
		return []brain.Message{output.Choices[0].Message}
	}
	return nil
}

func toolCallsFromMessages(messages []brain.Message) []brain.ToolCall {
	var calls []brain.ToolCall
	for _, message := range messages {
		calls = append(calls, message.ToolCalls...)
	}
	return calls
}
