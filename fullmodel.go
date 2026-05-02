package fullmodel

import (
	"context"
	"fmt"
	"strings"

	"github.com/xumi30/fullmodel/agent/brain"
	agentruntime "github.com/xumi30/fullmodel/agent/runtime"
	agenttools "github.com/xumi30/fullmodel/agent/tools"
	"github.com/xumi30/fullmodel/processmessage"
	"github.com/xumi30/fullmodel/utils/fileop"
)

type Client struct {
	runner   *agentruntime.Runner
	sessions agentruntime.SessionMemory
}

type Option func(*clientOptions)

type clientOptions struct {
	configFile string
	configs    *fileop.BrainConfigs
	sessions   agentruntime.SessionMemory
	tools      agentruntime.ToolExecutor
}

type RunOption func(*runOptions)

type runOptions struct {
	sessionID string
	stream    bool
	options   processmessage.Options
}

func Open(opts ...Option) (*Client, error) {
	options := clientOptions{
		sessions: agentruntime.NewSessionStore(),
		tools:    agentruntime.NewToolRegistryExecutor(agenttools.Getregistry()),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	cfgs := options.configs
	var err error
	if cfgs == nil {
		if strings.TrimSpace(options.configFile) != "" {
			cfgs, err = fileop.LoadBrainConfigsFromFile(options.configFile)
		} else {
			cfgs, err = fileop.LoadBrainConfigs()
		}
		if err != nil {
			return nil, err
		}
	}

	registry, err := agentruntime.NewRegistryFromConfigs(cfgs)
	if err != nil {
		return nil, err
	}
	return &Client{
		runner:   agentruntime.NewRunner(registry, options.tools),
		sessions: options.sessions,
	}, nil
}

func WithConfigFile(path string) Option {
	return func(opts *clientOptions) {
		opts.configFile = path
	}
}

func WithConfigs(configs *fileop.BrainConfigs) Option {
	return func(opts *clientOptions) {
		opts.configs = configs
	}
}

func WithSessionMemory(memory agentruntime.SessionMemory) Option {
	return func(opts *clientOptions) {
		if memory != nil {
			opts.sessions = memory
		}
	}
}

func WithToolExecutor(executor agentruntime.ToolExecutor) Option {
	return func(opts *clientOptions) {
		opts.tools = executor
	}
}

func WithSession(sessionID string) RunOption {
	return func(opts *runOptions) {
		opts.sessionID = sessionID
	}
}

func WithStream(stream bool) RunOption {
	return func(opts *runOptions) {
		opts.stream = stream
	}
}

func WithProcessOptions(options processmessage.Options) RunOption {
	return func(opts *runOptions) {
		opts.options = options
	}
}

func (c *Client) Run(ctx context.Context, message processmessage.Message, opts ...RunOption) (*agentruntime.Result, error) {
	if c == nil || c.runner == nil {
		return nil, fmt.Errorf("fullmodel client is nil")
	}
	runOpts := runOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&runOpts)
		}
	}
	options := runOpts.options
	options.Stream = runOpts.stream || options.Stream
	if options.Context == nil {
		options.Context = ctx
	}

	message = c.attachSession(message, runOpts.sessionID)
	result, err := c.runner.Run(ctx, agentruntime.Request{Message: message, Options: options})
	if err != nil {
		return nil, err
	}
	c.remember(message, runOpts.sessionID, result.Output)
	return result, nil
}

func (c *Client) Text(ctx context.Context, text string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.TextMessage{Text: text}, opts...)
	if err != nil {
		return "", err
	}
	if result == nil || result.Output == nil {
		return "", nil
	}
	return result.Output.Content.Text, nil
}

func (c *Client) Chat(ctx context.Context, sessionID, text string, opts ...RunOption) (string, error) {
	opts = append([]RunOption{WithSession(sessionID)}, opts...)
	return c.Text(ctx, text, opts...)
}

func (c *Client) GenerateImage(ctx context.Context, prompt string, opts ...RunOption) (*agentruntime.Result, error) {
	return c.Run(ctx, processmessage.ImageGenerateMessage{Prompt: prompt}, opts...)
}

func (c *Client) Transcribe(ctx context.Context, audio brain.MediaResource, opts ...RunOption) (*agentruntime.Result, error) {
	return c.Run(ctx, processmessage.SpeechToTextMessage{Audio: audio}, opts...)
}

func (c *Client) Capabilities() []agentruntime.Capability {
	if c == nil || c.runner == nil || c.runner.Registry() == nil {
		return nil
	}
	return c.runner.Registry().Capabilities()
}

func (c *Client) attachSession(message processmessage.Message, sessionID string) processmessage.Message {
	if c == nil || c.sessions == nil || strings.TrimSpace(sessionID) == "" {
		return message
	}
	switch msg := message.(type) {
	case processmessage.TextMessage:
		msg.History = c.sessions.Messages(sessionID)
		return msg
	case *processmessage.TextMessage:
		cp := *msg
		cp.History = c.sessions.Messages(sessionID)
		return &cp
	default:
		return message
	}
}

func (c *Client) remember(message processmessage.Message, sessionID string, out *brain.BrainOutput) {
	if c == nil || c.sessions == nil || strings.TrimSpace(sessionID) == "" || out == nil || !out.Status.Success {
		return
	}
	msg, ok := message.(processmessage.TextMessage)
	if !ok {
		if ptr, ptrOK := message.(*processmessage.TextMessage); ptrOK && ptr != nil {
			msg = *ptr
			ok = true
		}
	}
	if !ok {
		return
	}
	if strings.TrimSpace(msg.Text) != "" {
		c.sessions.Append(sessionID, brain.NewUserMessage(msg.Text))
	}
	if strings.TrimSpace(out.Content.Text) != "" {
		c.sessions.Append(sessionID, brain.NewAssistantMessage(out.Content.Text))
	}
}
