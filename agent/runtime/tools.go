package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xumi30/fullmodel/agent/brain"
	agenttools "github.com/xumi30/fullmodel/agent/tools"
)

// ToolExecutor executes model-requested tool calls.
type ToolExecutor interface {
	Tools() []brain.Tool
	Execute(ctx context.Context, call brain.ToolCall) (string, error)
}

// ToolRegistryExecutor adapts agent/tools.Registry to the runtime tool loop.
type ToolRegistryExecutor struct {
	registry interface {
		ConvertTools() []brain.Tool
		Get(name string) (agenttools.Tool, bool)
	}
}

func NewToolRegistryExecutor(registry interface {
	ConvertTools() []brain.Tool
	Get(name string) (agenttools.Tool, bool)
}) *ToolRegistryExecutor {
	return &ToolRegistryExecutor{registry: registry}
}

func (e *ToolRegistryExecutor) Tools() []brain.Tool {
	if e == nil || e.registry == nil {
		return nil
	}
	return e.registry.ConvertTools()
}

func (e *ToolRegistryExecutor) Execute(ctx context.Context, call brain.ToolCall) (string, error) {
	if e == nil || e.registry == nil {
		return "", fmt.Errorf("tool executor is nil")
	}
	name := call.Function.Name
	if name == "" {
		return "", fmt.Errorf("tool call has no function name")
	}
	tool, ok := e.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("%w: %s", agenttools.ErrFunctionNotFound, name)
	}
	result, err := tool.Execute(ctx, call.Function.Arguments)
	if err != nil {
		return "", err
	}
	return result, nil
}

func toolErrorPayload(err error) string {
	payload := map[string]any{
		"error": err.Error(),
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return err.Error()
	}
	return string(data)
}
