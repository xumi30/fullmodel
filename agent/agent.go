package agent

import (
	"context"
	"github.com/xumi30/fullmodel/agent/brain"
)

type Agent struct {
	SystemPrompts string
	brain         brain.Brain

	whatHappened []brain.Message
	tools        []brain.Tool
}

func NewAgent(systemPrompts string, brain brain.Brain, whatHappened []brain.Message, tools []brain.Tool) *Agent {
	return &Agent{
		SystemPrompts: systemPrompts,
		brain:         brain,
		whatHappened:  whatHappened,
		tools:         tools,
	}
}

func (a *Agent) BrainProcess(isStream bool) (output *brain.BrainOutput, err error) {

	if a.SystemPrompts != "" {
		a.whatHappened = append([]brain.Message{{Role: "system", Content: a.SystemPrompts}}, a.whatHappened...)
	}
	req := &brain.BrainInput{
		Context:  context.Background(),
		Mode:     brain.BrainModeText,
		Messages: a.whatHappened,
		Tools:    a.tools,
		Options: brain.BrainOptions{
			Stream: isStream,
		},
	}

	result, err := a.brain.ProcessInput(req)
	if err != nil {
		return nil, err
	}
	return result, nil
}
