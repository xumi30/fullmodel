package fullmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/xumi30/fullmodel/agent/brain"
	agentruntime "github.com/xumi30/fullmodel/agent/runtime"
	agenttools "github.com/xumi30/fullmodel/agent/tools"
	"github.com/xumi30/fullmodel/agent/tools/builtins"
	"github.com/xumi30/fullmodel/processmessage"
	"github.com/xumi30/fullmodel/utils/fileop"
	"github.com/xumi30/fullmodel/utils/logging"
)

// Client is the high-level SDK entrypoint for applications.
type Client struct {
	runner   *agentruntime.Runner
	sessions agentruntime.SessionMemory
	tools    agentruntime.ToolExecutor
}

// Memory is the SDK-facing session memory manager.
type Memory struct {
	store agentruntime.SessionMemory
}

// ToolHandler executes a model-requested tool call.
type ToolHandler func(ctx context.Context, arguments string) (string, error)

// SDKTool is the simple application-facing tool definition.
type SDKTool struct {
	Name        string
	Description string
	Parameters  any
	Handler     ToolHandler
}

// ToolSet is a small in-process tool executor for SDK users.
type ToolSet struct {
	mu    sync.RWMutex
	order []string
	tools map[string]SDKTool
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

// Open creates a ready-to-use FullModel client from config/llm.yaml.
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
	builtins.Register(agenttools.Getregistry())

	registry, err := agentruntime.NewRegistryFromConfigs(cfgs)
	if err != nil {
		return nil, err
	}
	return &Client{
		runner:   agentruntime.NewRunner(registry, options.tools),
		sessions: options.sessions,
		tools:    options.tools,
	}, nil
}

// MustOpen is a convenience helper for examples and small apps.
func MustOpen(opts ...Option) *Client {
	client, err := Open(opts...)
	if err != nil {
		panic(err)
	}
	return client
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

func WithTools(tools ...SDKTool) Option {
	return func(opts *clientOptions) {
		opts.tools = NewToolSet(tools...)
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

func WithRuntimeTools(enabled bool) RunOption {
	return func(opts *runOptions) {
		opts.options.DisableDefaultTools = !enabled
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

func (c *Client) StreamText(ctx context.Context, text string, opts ...RunOption) (brain.StreamOutput, error) {
	logging.Info("sdk StreamText start text_len=%d opts=%d", len(text), len(opts))
	opts = append(opts, WithStream(true), WithRuntimeTools(false))
	result, err := c.Run(ctx, processmessage.TextMessage{Text: text}, opts...)
	if err != nil {
		logging.Error("sdk StreamText run failed: %v", err)
		return nil, err
	}
	if result == nil || result.Output == nil {
		logging.Warn("sdk StreamText got empty result output")
		return nil, nil
	}
	if result.Output.Stream == nil {
		logging.Warn("sdk StreamText got nil stream mode=%s success=%v error=%q text_len=%d choices=%d",
			result.Output.Mode,
			result.Output.Status.Success,
			result.Output.Status.Error,
			len(result.Output.Content.Text),
			len(result.Output.Choices),
		)
		return nil, nil
	}
	logging.Info("sdk StreamText stream ready mode=%s success=%v choices=%d", result.Output.Mode, result.Output.Status.Success, len(result.Output.Choices))
	return result.Output.Stream, nil
}

func (c *Client) TextStream(ctx context.Context, text string, opts ...RunOption) (brain.StreamOutput, error) {
	return c.StreamText(ctx, text, opts...)
}

func (c *Client) Chat(ctx context.Context, sessionID, text string, opts ...RunOption) (string, error) {
	opts = append(opts, WithSession(sessionID))
	return c.Text(ctx, text, opts...)
}

func (c *Client) ClearSession(sessionID string) {
	c.Memory().Clear(sessionID)
}

func (c *Client) Image(ctx context.Context, image brain.MediaResource, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.ImageMessage{Prompt: prompt, Image: image}, opts...)
	if err != nil {
		return "", err
	}
	return textFromResult(result), nil
}

func (c *Client) Video(ctx context.Context, video brain.MediaResource, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.VideoMessage{Prompt: prompt, Video: video}, opts...)
	if err != nil {
		return "", err
	}
	return textFromResult(result), nil
}

func (c *Client) GenerateImage(ctx context.Context, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.ImageGenerateMessage{Prompt: prompt}, opts...)
	if err != nil {
		return "", err
	}
	return imageURLFromResult(result), nil
}

func (c *Client) EditImage(ctx context.Context, image brain.MediaResource, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.ImageEditMessage{
		Prompt: prompt,
		Images: []brain.MediaResource{image},
	}, opts...)
	if err != nil {
		return "", err
	}
	return imageURLFromResult(result), nil
}

func (c *Client) TextToVideo(ctx context.Context, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.TextToVideoMessage{Prompt: prompt}, opts...)
	if err != nil {
		return "", err
	}
	return videoURLFromResult(result), nil
}

func (c *Client) ImageToVideo(ctx context.Context, firstFrame brain.MediaResource, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.ImageToVideoMessage{
		Prompt:     prompt,
		FirstFrame: firstFrame,
	}, opts...)
	if err != nil {
		return "", err
	}
	return videoURLFromResult(result), nil
}

func (c *Client) TTS(ctx context.Context, text string, opts ...RunOption) ([]byte, error) {
	result, err := c.Run(ctx, processmessage.TextToSpeechMessage{Text: text}, opts...)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Output == nil {
		return nil, nil
	}
	return result.Output.Content.Audio.Data, nil
}

func (c *Client) ASR(ctx context.Context, audio brain.MediaResource, opts ...RunOption) (string, error) {
	result, err := c.Transcribe(ctx, audio, opts...)
	if err != nil {
		return "", err
	}
	return textFromResult(result), nil
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

func (c *Client) Tools() []brain.Tool {
	if c == nil || c.tools == nil {
		return nil
	}
	return c.tools.Tools()
}

func (c *Client) ExecuteTool(ctx context.Context, call brain.ToolCall) (string, error) {
	if c == nil || c.tools == nil {
		return "", fmt.Errorf("tool executor is nil")
	}
	return c.tools.Execute(ctx, call)
}

func (c *Client) Memory() *Memory {
	if c == nil {
		return &Memory{}
	}
	return &Memory{store: c.sessions}
}

func (m *Memory) Messages(sessionID string) []brain.Message {
	if m == nil || m.store == nil {
		return nil
	}
	return m.store.Messages(sessionID)
}

func (m *Memory) Append(sessionID string, messages ...brain.Message) {
	if m == nil || m.store == nil {
		return
	}
	m.store.Append(sessionID, messages...)
}

func (m *Memory) Replace(sessionID string, messages []brain.Message) {
	if m == nil || m.store == nil {
		return
	}
	m.store.Replace(sessionID, messages)
}

func (m *Memory) Clear(sessionID string) {
	if m == nil || m.store == nil {
		return
	}
	m.store.Clear(sessionID)
}

func (m *Memory) RememberUser(sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.Append(sessionID, brain.NewUserMessage(text))
}

func (m *Memory) RememberAssistant(sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.Append(sessionID, brain.NewAssistantMessage(text))
}

func (m *Memory) RememberSystem(sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.Append(sessionID, brain.NewSystemMessage(text))
}

func NewTool(name, description string, parameters any, handler ToolHandler) SDKTool {
	return SDKTool{
		Name:        name,
		Description: description,
		Parameters:  parameters,
		Handler:     handler,
	}
}

func NewToolSet(tools ...SDKTool) *ToolSet {
	set := &ToolSet{
		tools: make(map[string]SDKTool),
	}
	for _, tool := range tools {
		_ = set.Register(tool)
	}
	return set
}

func (s *ToolSet) Register(tool SDKTool) error {
	if s == nil {
		return fmt.Errorf("tool set is nil")
	}
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if tool.Handler == nil {
		return fmt.Errorf("tool %q handler is required", name)
	}
	tool.Name = name

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tools == nil {
		s.tools = make(map[string]SDKTool)
	}
	if _, ok := s.tools[name]; !ok {
		s.order = append(s.order, name)
	}
	s.tools[name] = tool
	return nil
}

func (s *ToolSet) Tools() []brain.Tool {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]brain.Tool, 0, len(s.order))
	for _, name := range s.order {
		tool, ok := s.tools[name]
		if !ok {
			continue
		}
		out = append(out, brain.Tool{
			Type: brain.ToolTypeFunction,
			Function: brain.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return out
}

func (s *ToolSet) Execute(ctx context.Context, call brain.ToolCall) (string, error) {
	if s == nil {
		return "", fmt.Errorf("tool set is nil")
	}
	name := strings.TrimSpace(call.Function.Name)
	if name == "" {
		return "", fmt.Errorf("tool call has no function name")
	}

	s.mu.RLock()
	tool, ok := s.tools[name]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("tool not found: %s", name)
	}
	return tool.Handler(ctx, call.Function.Arguments)
}

func DecodeToolArguments(arguments string, v any) error {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	return json.Unmarshal([]byte(arguments), v)
}

func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func MediaFromURL(url string) brain.MediaResource {
	return brain.MediaResource{URL: strings.TrimSpace(url)}
}

func MediaFromFile(path string) (brain.MediaResource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return brain.MediaResource{}, err
	}
	return brain.MediaResource{
		Data:     data,
		MimeType: DetectMime(path, data),
	}, nil
}

func MustMediaFromFile(path string) brain.MediaResource {
	media, err := MediaFromFile(path)
	if err != nil {
		panic(err)
	}
	return media
}

func DetectMime(path string, data []byte) string {
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

func (c *Client) attachSession(message processmessage.Message, sessionID string) processmessage.Message {
	if c == nil || c.sessions == nil || strings.TrimSpace(sessionID) == "" {
		return message
	}
	history := c.sessions.Messages(sessionID)
	switch msg := message.(type) {
	case processmessage.TextMessage:
		msg.History = mergeHistory(history, msg.History)
		return msg
	case *processmessage.TextMessage:
		cp := *msg
		cp.History = mergeHistory(history, cp.History)
		return &cp
	case processmessage.ChatMessage:
		msg.Messages = mergeHistory(history, msg.Messages)
		return msg
	case *processmessage.ChatMessage:
		cp := *msg
		cp.Messages = mergeHistory(history, cp.Messages)
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

func textFromResult(result *agentruntime.Result) string {
	if result == nil || result.Output == nil {
		return ""
	}
	return result.Output.Content.Text
}

func imageURLFromResult(result *agentruntime.Result) string {
	if result == nil || result.Output == nil {
		return ""
	}
	return result.Output.Content.Image.URL
}

func videoURLFromResult(result *agentruntime.Result) string {
	if result == nil || result.Output == nil {
		return ""
	}
	return result.Output.Content.Video.URL
}

func mergeHistory(stored, inline []brain.Message) []brain.Message {
	if len(stored) == 0 {
		return append([]brain.Message(nil), inline...)
	}
	if len(inline) == 0 {
		return append([]brain.Message(nil), stored...)
	}
	out := make([]brain.Message, 0, len(stored)+len(inline))
	out = append(out, stored...)
	out = append(out, inline...)
	return out
}
