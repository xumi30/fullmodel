package tools

import (
	"context"
	"errors"
)

var (
	ErrFunctionNotFound = errors.New("function not found")
	ErrInvalidParams    = errors.New("invalid parameters")
	ErrExecutionFailed  = errors.New("function execution failed")
)

// Tool represents a tool that can be used by an agent
type Tool interface {
	// Name returns the name of the tool
	Name() string

	// Description returns a description of what the tool does
	Description() string

	// Run executes the tool with the given input
	// Run(ctx context.Context, input string) (string, error)

	// Parameters returns the parameters that the tool accepts
	Parameters() map[string]interface{}

	// Execute executes the tool with the given arguments
	Execute(ctx context.Context, args string) (string, error)

	Results() map[string]interface{}

	// SimpleInfo returns a short topic label and one-line description for UIs or lexicons.
	// Keys: "topic", "simpledescription" (JSON field names).
	SimpleInfo() map[string]string
}
