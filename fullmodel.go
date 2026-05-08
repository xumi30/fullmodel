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
// Create one with Open, then reuse it across your app handlers or services.
type Client struct {
	runner   *agentruntime.Runner
	sessions agentruntime.SessionMemory
	tools    agentruntime.ToolExecutor
}

// Memory is the SDK-facing session memory manager.
// It exposes the same session store used by Chat and WithSession.
type Memory struct {
	store agentruntime.SessionMemory
}

// ToolHandler executes a model-requested tool call.
// The arguments string is the raw JSON object emitted by the model.
type ToolHandler func(ctx context.Context, arguments string) (string, error)

// SDKTool is the simple application-facing tool definition.
type SDKTool struct {
	// Name is the function name exposed to the model.
	Name string
	// Description tells the model when and how to use this tool.
	Description string
	// Parameters is a JSON Schema object describing accepted arguments.
	Parameters any
	// Handler executes the tool and returns text to feed back to the model.
	Handler ToolHandler
}

// ToolSet is a small in-process tool executor for SDK users.
type ToolSet struct {
	mu    sync.RWMutex
	order []string
	tools map[string]SDKTool
}

type memoryStream struct {
	brain.StreamOutput
	textCh    chan string
	waitOnce  sync.Once
	waitErr   error
	cancel    context.CancelFunc
	userText  string
	sessionID string
	memory    *Memory
	collected strings.Builder
	done      chan struct{}
}

// Option configures a Client during Open.
type Option func(*clientOptions)

type clientOptions struct {
	configFile string
	configs    *fileop.BrainConfigs
	sessions   agentruntime.SessionMemory
	tools      agentruntime.ToolExecutor
}

// RunOption configures one SDK call, such as Text, Chat, StreamText, or TTS.
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

// WithConfigFile makes Open load model configuration from path.
// Relative paths are resolved through the project's config lookup rules.
func WithConfigFile(path string) Option {
	return func(opts *clientOptions) {
		opts.configFile = path
	}
}

// WithConfigs makes Open use an already-built model configuration.
func WithConfigs(configs *fileop.BrainConfigs) Option {
	return func(opts *clientOptions) {
		opts.configs = configs
	}
}

// WithSessionMemory replaces the default in-memory session store.
// Use this to plug in file-backed or application-owned memory.
func WithSessionMemory(memory agentruntime.SessionMemory) Option {
	return func(opts *clientOptions) {
		if memory != nil {
			opts.sessions = memory
		}
	}
}

// WithToolExecutor replaces the default tool executor.
// It is useful when your application already owns a tool registry.
func WithToolExecutor(executor agentruntime.ToolExecutor) Option {
	return func(opts *clientOptions) {
		opts.tools = executor
	}
}

// WithTools installs a small in-process tool set for this client.
// These tools participate in the non-streaming Text/Chat tool loop.
func WithTools(tools ...SDKTool) Option {
	return func(opts *clientOptions) {
		opts.tools = NewToolSet(tools...)
	}
}

// WithSession attaches a session ID to one call.
// Stored session messages are prepended before the new user message.
func WithSession(sessionID string) RunOption {
	return func(opts *runOptions) {
		opts.sessionID = sessionID
	}
}

// WithStream requests streaming output for calls made through Run.
// Prefer StreamText for the high-level text streaming shortcut.
func WithStream(stream bool) RunOption {
	return func(opts *runOptions) {
		opts.stream = stream
	}
}

// WithProcessOptions replaces the low-level process options for one call.
func WithProcessOptions(options processmessage.Options) RunOption {
	return func(opts *runOptions) {
		opts.options = options
	}
}

// WithRuntimeTools controls whether the client's default tool executor is
// injected when no explicit tools are supplied for a call.
func WithRuntimeTools(enabled bool) RunOption {
	return func(opts *runOptions) {
		opts.options.DisableDefaultTools = !enabled
	}
}

// WithTTSVoice sets the CosyVoice voice parameter for one TTS call.
func WithTTSVoice(voice string) RunOption {
	return withExtra("voice", strings.TrimSpace(voice))
}

// WithTTSFormat sets the TTS audio format, such as mp3, wav, or pcm.
func WithTTSFormat(format string) RunOption {
	return withExtra("format", strings.TrimSpace(format))
}

// WithTTSSampleRate sets the TTS sample rate in Hz.
func WithTTSSampleRate(sampleRate int) RunOption {
	return withExtra("sample_rate", sampleRate)
}

// WithTTSVolume sets the TTS volume parameter.
func WithTTSVolume(volume int) RunOption {
	return withExtra("volume", volume)
}

// WithTTSRate sets the TTS speech rate multiplier.
func WithTTSRate(rate float64) RunOption {
	return withExtra("rate", rate)
}

// WithTTSPitch sets the TTS pitch multiplier.
func WithTTSPitch(pitch float64) RunOption {
	return withExtra("pitch", pitch)
}

// WithTTSSSML enables or disables SSML input for one TTS call.
func WithTTSSSML(enabled bool) RunOption {
	return withExtra("enable_ssml", enabled)
}

// Run executes a processmessage.Message through the runtime.
// It is the escape hatch for SDK users who need full control over message types.
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

// Text sends a single user text prompt and returns the assistant text.
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

// StreamText sends a single user text prompt and returns a streaming output.
// It does not collect chunks for you; consume stream.Text and then call Wait.
// Runtime default tools are disabled by default so plain text streaming cannot
// be intercepted by a model tool call.
// If WithSession is supplied, the user prompt is remembered immediately; callers
// must remember the assistant response themselves or use StreamChat.
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

// TextStream is an alias for StreamText.
func (c *Client) TextStream(ctx context.Context, text string, opts ...RunOption) (brain.StreamOutput, error) {
	return c.StreamText(ctx, text, opts...)
}

// Chat sends text in a named session and returns the assistant text.
// It automatically reads and writes the session history.
func (c *Client) Chat(ctx context.Context, sessionID, text string, opts ...RunOption) (string, error) {
	opts = append(opts, WithSession(sessionID))
	return c.Text(ctx, text, opts...)
}

// StreamChat sends text in a named session and returns a streaming output.
// It automatically remembers both the user prompt and the complete assistant
// response after the stream finishes successfully.
func (c *Client) StreamChat(ctx context.Context, sessionID, text string, opts ...RunOption) (brain.StreamOutput, error) {
	opts = append(opts, WithSession(sessionID))
	stream, err := c.StreamText(ctx, text, opts...)
	if err != nil {
		return nil, err
	}
	if stream == nil || strings.TrimSpace(sessionID) == "" {
		return stream, nil
	}
	return newMemoryStream(stream, c.Memory(), sessionID, text), nil
}

// ClearSession deletes all remembered messages for a session.
func (c *Client) ClearSession(sessionID string) {
	c.Memory().Clear(sessionID)
}

// Image asks a vision-capable model to answer a prompt about an image.
func (c *Client) Image(ctx context.Context, image brain.MediaResource, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.ImageMessage{Prompt: prompt, Image: image}, opts...)
	if err != nil {
		return "", err
	}
	return textFromResult(result), nil
}

// Video asks a vision-capable model to answer a prompt about a video.
func (c *Client) Video(ctx context.Context, video brain.MediaResource, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.VideoMessage{Prompt: prompt, Video: video}, opts...)
	if err != nil {
		return "", err
	}
	return textFromResult(result), nil
}

// GenerateImage generates an image from a text prompt and returns its URL.
func (c *Client) GenerateImage(ctx context.Context, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.ImageGenerateMessage{Prompt: prompt}, opts...)
	if err != nil {
		return "", err
	}
	return imageURLFromResult(result), nil
}

// EditImage edits an image with a text instruction and returns the output URL.
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

// TextToVideo generates a video from a text prompt and returns its URL.
func (c *Client) TextToVideo(ctx context.Context, prompt string, opts ...RunOption) (string, error) {
	result, err := c.Run(ctx, processmessage.TextToVideoMessage{Prompt: prompt}, opts...)
	if err != nil {
		return "", err
	}
	return videoURLFromResult(result), nil
}

// ImageToVideo generates a video from a first-frame image and prompt.
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

// TTS synthesizes speech from text and returns audio bytes.
// Use WithTTSVoice and related RunOptions to choose voice and audio parameters.
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

// ASR transcribes an audio resource and returns text.
func (c *Client) ASR(ctx context.Context, audio brain.MediaResource, opts ...RunOption) (string, error) {
	result, err := c.Transcribe(ctx, audio, opts...)
	if err != nil {
		return "", err
	}
	return textFromResult(result), nil
}

// Transcribe runs the lower-level speech-to-text request and returns the full result.
func (c *Client) Transcribe(ctx context.Context, audio brain.MediaResource, opts ...RunOption) (*agentruntime.Result, error) {
	return c.Run(ctx, processmessage.SpeechToTextMessage{Audio: audio}, opts...)
}

// Capabilities returns the model capabilities registered for this client.
func (c *Client) Capabilities() []agentruntime.Capability {
	if c == nil || c.runner == nil || c.runner.Registry() == nil {
		return nil
	}
	return c.runner.Registry().Capabilities()
}

// Tools returns the tools exposed by this client's tool executor.
func (c *Client) Tools() []brain.Tool {
	if c == nil || c.tools == nil {
		return nil
	}
	return c.tools.Tools()
}

// ExecuteTool executes a tool call directly through this client's tool executor.
// This is mainly useful for testing or debugging tool handlers.
func (c *Client) ExecuteTool(ctx context.Context, call brain.ToolCall) (string, error) {
	if c == nil || c.tools == nil {
		return "", fmt.Errorf("tool executor is nil")
	}
	return c.tools.Execute(ctx, call)
}

// Memory returns the session memory manager used by this client.
func (c *Client) Memory() *Memory {
	if c == nil {
		return &Memory{}
	}
	return &Memory{store: c.sessions}
}

// Messages returns a copy of remembered messages for a session.
func (m *Memory) Messages(sessionID string) []brain.Message {
	if m == nil || m.store == nil {
		return nil
	}
	return m.store.Messages(sessionID)
}

// Append appends one or more messages to a session.
func (m *Memory) Append(sessionID string, messages ...brain.Message) {
	if m == nil || m.store == nil {
		return
	}
	m.store.Append(sessionID, messages...)
}

// Replace replaces all remembered messages for a session.
func (m *Memory) Replace(sessionID string, messages []brain.Message) {
	if m == nil || m.store == nil {
		return
	}
	m.store.Replace(sessionID, messages)
}

// Clear removes all remembered messages for a session.
func (m *Memory) Clear(sessionID string) {
	if m == nil || m.store == nil {
		return
	}
	m.store.Clear(sessionID)
}

// RememberUser appends a user message to a session.
func (m *Memory) RememberUser(sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.Append(sessionID, brain.NewUserMessage(text))
}

// RememberAssistant appends an assistant message to a session.
func (m *Memory) RememberAssistant(sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.Append(sessionID, brain.NewAssistantMessage(text))
}

// RememberSystem appends a system message to a session.
func (m *Memory) RememberSystem(sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.Append(sessionID, brain.NewSystemMessage(text))
}

func newMemoryStream(stream brain.StreamOutput, memory *Memory, sessionID, userText string) *memoryStream {
	ctx, cancel := context.WithCancel(context.Background())
	out := &memoryStream{
		StreamOutput: stream,
		textCh:       make(chan string),
		cancel:       cancel,
		memory:       memory,
		sessionID:    sessionID,
		userText:     userText,
		done:         make(chan struct{}),
	}
	go out.forwardText(ctx)
	return out
}

func (s *memoryStream) Text() <-chan string {
	if s == nil {
		return nil
	}
	return s.textCh
}

func (s *memoryStream) Cancel() {
	if s == nil {
		return
	}
	s.cancel()
	s.StreamOutput.Cancel()
}

func (s *memoryStream) Wait() error {
	if s == nil {
		return nil
	}
	s.waitOnce.Do(func() {
		<-s.done
		s.waitErr = s.StreamOutput.Wait()
		if s.waitErr == nil {
			s.rememberAssistant()
		}
	})
	return s.waitErr
}

func (s *memoryStream) forwardText(ctx context.Context) {
	defer close(s.textCh)
	defer close(s.done)

	for chunk := range s.StreamOutput.Text() {
		s.collected.WriteString(chunk)
		select {
		case <-ctx.Done():
			return
		case s.textCh <- chunk:
		}
	}
}

func (s *memoryStream) rememberAssistant() {
	if s == nil || s.memory == nil {
		return
	}
	text := s.collected.String()
	if strings.TrimSpace(text) == "" {
		return
	}
	s.memory.RememberAssistant(s.sessionID, text)
}

// NewTool creates a simple SDK tool definition.
func NewTool(name, description string, parameters any, handler ToolHandler) SDKTool {
	return SDKTool{
		Name:        name,
		Description: description,
		Parameters:  parameters,
		Handler:     handler,
	}
}

// NewToolSet creates an in-process tool executor from SDK tools.
func NewToolSet(tools ...SDKTool) *ToolSet {
	set := &ToolSet{
		tools: make(map[string]SDKTool),
	}
	for _, tool := range tools {
		_ = set.Register(tool)
	}
	return set
}

// Register adds or replaces a tool in this set.
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

// Tools converts this set into model-facing tool definitions.
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

// Execute runs the handler for a model tool call.
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

// DecodeToolArguments unmarshals a tool call's JSON argument string into v.
// Empty arguments are treated as an empty JSON object.
func DecodeToolArguments(arguments string, v any) error {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	return json.Unmarshal([]byte(arguments), v)
}

// ObjectSchema builds a small JSON Schema object for function tool parameters.
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

// MediaFromURL creates a media resource that points to a remote URL.
func MediaFromURL(url string) brain.MediaResource {
	return brain.MediaResource{URL: strings.TrimSpace(url)}
}

// MediaFromFile reads a local media file and returns an inline media resource.
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

// MustMediaFromFile is like MediaFromFile but panics on error.
func MustMediaFromFile(path string) brain.MediaResource {
	media, err := MediaFromFile(path)
	if err != nil {
		panic(err)
	}
	return media
}

// DetectMime detects a media MIME type from bytes or, if empty, from extension.
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

func withExtra(key string, value any) RunOption {
	return func(opts *runOptions) {
		if opts.options.Extra == nil {
			opts.options.Extra = make(map[string]any)
		}
		switch v := value.(type) {
		case string:
			if v == "" {
				return
			}
		case int:
			if v == 0 {
				return
			}
		case float64:
			if v == 0 {
				return
			}
		}
		opts.options.Extra[key] = value
	}
}
