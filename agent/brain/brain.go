package brain

import (
	"context"
	"encoding/json"
)

// Brain 大脑接口 - 统一的多模态处理接口
type Brain interface {
	// ProcessInput 统一的处理接口，处理所有类型的输入并返回结构化输出
	ProcessInput(input *BrainInput) (output *BrainOutput, err error)
}

// BrainInput 统一的输入结构体
type BrainInput struct {
	// 输入模式
	Mode BrainMode `json:"mode"`

	// 文本内容 - 用于文本处理和提示
	Text string `json:"text,omitempty"`

	// 结构化消息 - 用于复杂的多轮对话
	Messages []Message `json:"messages,omitempty"`

	Tools []Tool `json:"tools,omitempty"`

	// 多模态内容
	MultimodalParts []ContentPart `json:"multimodal_parts,omitempty"`

	// 二进制数据
	ImageData []byte `json:"-"` // 图像二进制数据
	AudioData []byte `json:"-"` // 音频二进制数据
	VideoData []byte `json:"-"` // 视频二进制数据

	// URL资源
	ImageURL string `json:"image_url,omitempty"` // 图像URL
	AudioURL string `json:"audio_url,omitempty"` // 音频URL
	VideoURL string `json:"video_url,omitempty"` // 视频URL

	// 配置参数
	Model       string   `json:"model,omitempty"`
	Stream      bool     `json:"stream,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`

	// 上下文信息
	Context context.Context `json:"-"`

	// 扩展参数
	ExtraParams map[string]any `json:"extra_params,omitempty"`
}

// BrainOutput 统一的输出结构体
type BrainOutput struct {
	// 输出模式
	Mode BrainMode `json:"mode"`

	// 文本输出 - 主要的处理结果
	Text string `json:"text,omitempty"`

	// 流式输出通道
	TextStream  <-chan string `json:"-"`
	ErrorStream <-chan error  `json:"-"`
	// ToolCallsStream 流式 Function Calling：每个 chunk 合并后的累积快照（arguments 按片段拼接）。
	// 与 TextStream 并发读取；无工具调用时通道关闭且不会有元素。
	ToolCallsStream <-chan []ToolCall `json:"-"`

	// 二进制输出
	ImageData []byte `json:"-"` // 生成的图像数据
	AudioData []byte `json:"-"` // 生成的音频数据
	VideoData []byte `json:"-"` // 生成的视频数据

	// 资源输出
	ImageURL string `json:"image_url,omitempty"` // 生成的图像URL
	AudioURL string `json:"audio_url,omitempty"` // 生成的音频URL
	VideoURL string `json:"video_url,omitempty"` // 生成的视频URL

	// 结构化输出
	Choices  []Choice  `json:"choices,omitempty"`
	Messages []Message `json:"messages,omitempty"`

	// 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// 处理统计
	Usage *Usage `json:"usage,omitempty"`

	// 处理状态
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// NewSystemMessage 创建系统消息
func NewSystemMessage(content string) Message {
	return Message{
		Role:    "system",
		Content: content,
	}
}

// NewUserMessage 创建用户消息
func NewUserMessage(content string) Message {
	return Message{
		Role:    "user",
		Content: content,
	}
}

// NewAssistantMessage 创建助手消息
func NewAssistantMessage(content string) Message {
	return Message{
		Role:    "assistant",
		Content: content,
	}
}

// NewToolMessage 创建工具消息
func NewToolMessage(toolCallID, content string) Message {
	return Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	}
}

// NewTextContentPart 创建文本内容部分
func NewTextContentPart(text string) ContentPart {
	return ContentPart{
		Type: "text",
		Text: text,
	}
}

// NewMultimodalUserMessage 创建多模态用户消息
func NewMultimodalUserMessage(parts ...ContentPart) Message {
	return Message{
		Role:    "user",
		Content: parts,
	}
}

// 模型名称常量
const (
	ModelQWenTurbo      = "qwen-turbo"
	ModelQWenPlus       = "qwen-plus"
	ModelQWenMax        = "qwen-max"
	ModelQWenVLTurbo    = "qwen-vl-turbo"
	ModelQWenVLPlus     = "qwen-vl-plus"
	ModelQWenVLMax      = "qwen-vl-max"
	ModelQWenAudioTurbo = "qwen-audio-turbo"
)

// 区域常量
const (
	RegionBeijing   = "cn-beijing"
	RegionSingapore = "ap-southeast-1"
	RegionVirginia  = "us-east-1"
)

// BrainMode 处理模式枚举
type BrainMode string

const (
	BrainModeText            BrainMode = "text"                         // 纯文本处理
	BrainModeImageUnderstand BrainMode = "image_understand"             // 图像理解模式
	BrainModeASR             BrainMode = "automatic speech recognition" // 音频处理
	BrainModeVideoUnderstand BrainMode = "video_understand"             // 视频理解模式
	BrainModeMultimodal      BrainMode = "multimodal"                   // 多模态处理
	BrainModeVoiceGenerate   BrainMode = "voice_generate"               // 语音生成模式
	BrainModeAnalyze         BrainMode = "analyze"                      // 分析模式
	BrainModeStream          BrainMode = "stream"                       // 流式模式
	BrainIMageGenerate       BrainMode = "image_generate"               // 图像生成模式
	BrainText2VideoGenerate  BrainMode = "video2text_generate"          // 文生视频生成模式
	BrainImage2VideoGenerate BrainMode = "image2video_generate"         // 图像生视频生成模式
	BrainText2SpeechGenerate BrainMode = "text2speech_generate"         // 文本转语音生成模式
	BrainVisualUnderstand    BrainMode = "visual_understand"            // 视觉理解模式
)

// Config 通用配置，支持多提供商
type Config struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	Region  string `yaml:"region"` // 地域: cn-beijing, ap-southeast-1, us-east-1

	// 提供商特定设置
	Provider     ProviderType      `yaml:"provider"`  // 提供商类型
	APIEndpoints map[string]string `yaml:"endpoints"` // 自定义端点映射
}

// ProviderType 提供商类型
type ProviderType string

// 支持的提供商常量
const (
	ProviderQwen     ProviderType = "qwen"     // 阿里云千问
	ProviderGeneric  ProviderType = "generic"  // 通用 OpenAI 兼容
	ProviderOpenAI   ProviderType = "openai"   // OpenAI 官方
	ProviderAzure    ProviderType = "azure"    // Azure OpenAI
	ProviderLocalAI  ProviderType = "localai"  // LocalAI 等本地部署
	ProviderGroq     ProviderType = "groq"     // Groq
	ProviderTogether ProviderType = "together" // Together AI
	ProviderCustom   ProviderType = "custom"   // 自定义提供商
)

// 为向后兼容保留的别名
type QwenConfig = Config

// Message 聊天消息结构（对齐 OpenAI / 千问兼容 Chat API）
type Message struct {
	Role    string `json:"role"`    // system, user, assistant, tool
	Content any    `json:"content"` // string 或 []ContentPart；含 tool_calls 时 assistant 可为 null/省略由服务端决定

	// assistant：思维链（enable_thinking 等）；流式 delta 同名字段
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// assistant：Function Calling 返回；回传上下文时需带齐
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// assistant：前缀续写（千问 partial）
	Partial *bool `json:"partial,omitempty"`

	// tool：必选，对应上一轮 assistant.tool_calls[].id
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ChatCompletionRequest 聊天完成请求
type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Seed        *int      `json:"seed,omitempty"`
	Stop        any       `json:"stop,omitempty"` // string 或 []string

	// 扩展参数 (通过 extra_body 传递)
	ExtraBody map[string]any `json:"-"`

	// 流式响应配置
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	// 多模态输出
	Modalities []string           `json:"modalities,omitempty"`
	Audio      *AudioOutputConfig `json:"audio,omitempty"`

	// 响应格式
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	// 工具调用
	Tools      []Tool `json:"tools,omitempty"`
	ToolChoice any    `json:"tool_choice,omitempty"` // string 或 ToolChoiceFunction

	// 联网搜索
	EnableSearch  *bool          `json:"-"`
	SearchOptions *SearchOptions `json:"-"`

	// 深度思考 (思考模式)
	EnableThinking   *bool `json:"-"`
	PreserveThinking *bool `json:"-"`
	ThinkingBudget   *int  `json:"-"`

	// 其他高级参数
	N                     int      `json:"n,omitempty"`
	TopK                  *int     `json:"-"`
	RepetitionPenalty     *float64 `json:"-"`
	PresencePenalty       *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty      *float64 `json:"frequency_penalty,omitempty"`
	LogProbs              *bool    `json:"logprobs,omitempty"`
	TopLogProbs           *int     `json:"top_logprobs,omitempty"`
	ParallelToolCalls     *bool    `json:"parallel_tool_calls,omitempty"`
	EnableCodeInterpreter *bool    `json:"-"`

	// 视觉：高分辨率策略（千问非 OpenAI 标准，HTTP 层置于请求根字段）
	VLHighResolutionImages *bool `json:"-"`
}

// StreamOptions 流式选项
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// AudioOutputConfig 音频输出配置
type AudioOutputConfig struct {
	Voice  string `json:"voice"`
	Format string `json:"format"` // wav
}

// ResponseFormat 响应格式
type ResponseFormat struct {
	Type string `json:"type"` // text, json_object
}

const (
	ToolTypeFunction  = "function"
	ToolTypeRetrieval = "retrieval"
	ToolTypeWebSearch = "web_search"
	ToolTypeMCP       = "mcp"
)

// Tool 工具定义
type Tool struct {
	Type     string             `json:"type"` // function
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition 函数定义
type FunctionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"` // JSON Schema
}

// ToolChoiceFunction 工具选择函数
type ToolChoiceFunction struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

// SearchOptions 搜索选项
type SearchOptions struct {
	ForcedSearch          bool   `json:"forced_search,omitempty"`
	SearchStrategy        string `json:"search_strategy,omitempty"` // turbo, max, agent
	EnableSearchExtension bool   `json:"enable_search_extension,omitempty"`
}

// ChatCompletionResponse 聊天完成响应
type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Choices           []Choice `json:"choices"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Object            string   `json:"object"`
	Usage             Usage    `json:"usage,omitempty"`
	SystemFingerprint *string  `json:"system_fingerprint,omitempty"`
	ServiceTier       *string  `json:"service_tier,omitempty"`
}

// Choice 选择结果
type Choice struct {
	Index        int       `json:"index"`
	Message      Message   `json:"message"`
	FinishReason string    `json:"finish_reason"` // stop, length, tool_calls
	LogProbs     *LogProbs `json:"logprobs,omitempty"`
}

// LogProbs 对数概率
type LogProbs struct {
	Content []LogProbsContent `json:"content"`
}

// LogProbsContent 对数概率内容
type LogProbsContent struct {
	Token       string       `json:"token"`
	Bytes       []byte       `json:"bytes"`
	Logprob     *float64     `json:"logprob"` // API 可能返回 null
	TopLogProbs []TopLogProb `json:"top_logprobs,omitempty"`
}

// TopLogProb 顶部对数概率
type TopLogProb struct {
	Token   string   `json:"token"`
	Bytes   []byte   `json:"bytes"`
	Logprob *float64 `json:"logprob"`
}

// Usage Token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	PromptTokensDetails     *TokensDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *TokensDetails `json:"completion_tokens_details,omitempty"`
}

// TokensDetails Token 详情
type TokensDetails struct {
	AudioTokens     *int           `json:"audio_tokens,omitempty"`
	CachedTokens    *int           `json:"cached_tokens,omitempty"`
	TextTokens      *int           `json:"text_tokens,omitempty"`
	ImageTokens     *int           `json:"image_tokens,omitempty"`
	VideoTokens     *int           `json:"video_tokens,omitempty"`
	ReasoningTokens *int           `json:"reasoning_tokens,omitempty"`
	CacheCreation   *CacheCreation `json:"cache_creation,omitempty"`
}

// CacheCreation 显式缓存创建信息（usage / prompt_tokens_details）
type CacheCreation struct {
	Ephemeral5mInputTokens   *int   `json:"ephemeral_5m_input_tokens,omitempty"`
	CacheCreationInputTokens *int   `json:"cache_creation_input_tokens,omitempty"`
	CacheType                string `json:"cache_type,omitempty"`
}

// ChatCompletionChunk 流式响应块
type ChatCompletionChunk struct {
	ID                string        `json:"id"`
	Choices           []ChunkChoice `json:"choices"`
	Created           int64         `json:"created"`
	Model             string        `json:"model"`
	Object            string        `json:"object"` // "chat.completion.chunk"
	Usage             *Usage        `json:"usage,omitempty"`
	SystemFingerprint *string       `json:"system_fingerprint,omitempty"`
	ServiceTier       *string       `json:"service_tier,omitempty"`
}

// ChunkChoice 流式选择块
type ChunkChoice struct {
	Index        int       `json:"index"`
	Delta        Delta     `json:"delta"`
	FinishReason string    `json:"finish_reason,omitempty"`
	LogProbs     *LogProbs `json:"logprobs,omitempty"`
}

// Delta 增量内容
type Delta struct {
	Content          string      `json:"content,omitempty"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
	Role             string      `json:"role,omitempty"`
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
	Audio            *DeltaAudio `json:"audio,omitempty"` // Qwen-Omni 流式音频增量
}

// DeltaAudio 流式响应中的音频增量（Qwen-Omni）
type DeltaAudio struct {
	Data      string `json:"data,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// ToolCall 工具调用
type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function,omitempty"`
}

// FunctionCall 函数调用
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ContentPart 多模态内容部分（基础定义）
type ContentPart struct {
	Type      string            `json:"type"` // text, image_url, image_data, input_audio, audio_data, video, video_url
	Text      string            `json:"text,omitempty"`
	ImageURL  *ContentImageURL  `json:"image_url,omitempty"`
	ImageData *ContentImageData `json:"image_data,omitempty"`
	Audio     *ContentAudio     `json:"input_audio,omitempty"`
	AudioData *ContentAudioData `json:"audio_data,omitempty"`
	Video     []string          `json:"video,omitempty"` // 图片列表形式
	VideoURL  *ContentVideoURL  `json:"video_url,omitempty"`
	VideoData *ContentVideoData `json:"video_data,omitempty"`

	// 与 type 同级：千问 VL 文档中 image_url / video / video_url 可带的像素与抽帧参数
	MinPixels   *int     `json:"min_pixels,omitempty"`
	MaxPixels   *int     `json:"max_pixels,omitempty"`
	TotalPixels *int     `json:"total_pixels,omitempty"`
	FPS         *float64 `json:"fps,omitempty"`

	// 显式缓存（多模态条目中）
	CacheControl *ContentCacheControl `json:"cache_control,omitempty"`
}

// ContentCacheControl 显式缓存控制（千问）
type ContentCacheControl struct {
	Type string `json:"type"` // ephemeral
	Role string `json:"role"` // user
}

// ContentImageURL 图像 URL
type ContentImageURL struct {
	URL string `json:"url"`
}

// ContentImageData 图像二进制数据
type ContentImageData struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type"`
}

// ContentAudio 音频内容
type ContentAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

// ContentVideoURL 视频 URL
type ContentVideoURL struct {
	URL         string  `json:"url"`
	FPS         float64 `json:"fps,omitempty"`
	MinPixels   int     `json:"min_pixels,omitempty"`
	MaxPixels   int     `json:"max_pixels,omitempty"`
	TotalPixels int     `json:"total_pixels,omitempty"`
}

// ContentAudioData 音频二进制数据
type ContentAudioData struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type"`
}

// ContentVideoData 视频二进制数据
type ContentVideoData struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type"`
}

// Float64Ptr 返回float64的指针
func Float64Ptr(f float64) *float64 {
	return &f
}

// IntPtr 返回int的指针
func IntPtr(i int) *int {
	return &i
}

// BoolPtr 返回bool的指针
func BoolPtr(b bool) *bool {
	return &b
}

// NewContentPart 创建内容部分的辅助函数
func NewContentPart(contentType string, content interface{}) ContentPart {
	part := ContentPart{Type: contentType}

	switch v := content.(type) {
	case string:
		part.Text = v
	case *ContentImageURL:
		part.ImageURL = v
	case *ContentImageData:
		part.ImageData = v
	case *ContentAudio:
		part.Audio = v
	case *ContentAudioData:
		part.AudioData = v
	case []string:
		part.Video = v
	case *ContentVideoURL:
		part.VideoURL = v
	case *ContentVideoData:
		part.VideoData = v
	}

	return part
}

// WithSearch 启用联网搜索
func (req *ChatCompletionRequest) WithSearch() *ChatCompletionRequest {
	req.EnableSearch = boolPtr(true)
	return req
}

// WithThinking 启用深度思考模式
func (req *ChatCompletionRequest) WithThinking(preserveThinking bool, thinkingBudget int) *ChatCompletionRequest {
	req.EnableThinking = boolPtr(true)
	req.PreserveThinking = boolPtr(preserveThinking)
	req.ThinkingBudget = &thinkingBudget
	return req
}

// WithJSONMode 启用 JSON 响应模式
func (req *ChatCompletionRequest) WithJSONMode() *ChatCompletionRequest {
	req.ResponseFormat = &ResponseFormat{Type: "json_object"}
	return req
}

// DefaultQwenConfig 创建默认配置
func DefaultQwenConfig(apiKey, model string) *QwenConfig {
	return &QwenConfig{
		APIKey:  apiKey,
		Model:   model,
		Region:  RegionBeijing,
		BaseURL: "", // 使用默认
	}
}

// Helper functions

func boolPtr(b bool) *bool {
	return &b
}

func float64Ptr(f float64) *float64 {
	return &f
}

func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}

func buildRequestBody(req ChatCompletionRequest) ([]byte, error) {
	// 创建基础请求体
	baseReq := map[string]any{
		"model":           req.Model,
		"messages":        req.Messages,
		"stream":          req.Stream,
		"enable_thinking": false, // 默认关闭深度思考，除非调用方显式启用
	}

	// 添加可选参数
	if req.Temperature != nil {
		baseReq["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		baseReq["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		baseReq["max_tokens"] = *req.MaxTokens
	}
	if req.Seed != nil {
		baseReq["seed"] = *req.Seed
	}
	// 修复：正确处理 Stop 参数
	if req.Stop != nil {
		// Stop 可能是 string 或 []string 类型
		if stopStr, ok := req.Stop.(string); ok {
			baseReq["stop"] = stopStr
		} else if stopSlice, ok := req.Stop.([]string); ok {
			baseReq["stop"] = stopSlice
		} else {
			baseReq["stop"] = req.Stop
		}
	}
	// 修改：确保 StreamOptions 被正确处理
	if req.StreamOptions != nil {
		baseReq["stream_options"] = map[string]any{
			"include_usage": req.StreamOptions.IncludeUsage,
		}
	}
	if req.ResponseFormat != nil {
		baseReq["response_format"] = req.ResponseFormat
	}
	if len(req.Tools) > 0 {
		baseReq["tools"] = req.Tools
	}
	if req.ToolChoice != nil {
		baseReq["tool_choice"] = req.ToolChoice
	}
	if req.N > 0 {
		baseReq["n"] = req.N
	}
	if req.PresencePenalty != nil {
		baseReq["presence_penalty"] = *req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		baseReq["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.LogProbs != nil {
		baseReq["logprobs"] = *req.LogProbs
	}
	if req.TopLogProbs != nil {
		baseReq["top_logprobs"] = *req.TopLogProbs
	}
	if req.Modalities != nil {
		baseReq["modalities"] = req.Modalities
	}
	if req.Audio != nil {
		baseReq["audio"] = req.Audio
	}
	if req.ParallelToolCalls != nil {
		baseReq["parallel_tool_calls"] = *req.ParallelToolCalls
	}

	// 合并 ExtraBody 与千问扩展字段到请求根级（与官方 curl / HTTP 示例一致；DashScope 不识别整包 extra_body）
	merge := make(map[string]any)
	if req.ExtraBody != nil {
		for k, v := range req.ExtraBody {
			merge[k] = v
		}
	}
	if req.TopK != nil {
		merge["top_k"] = *req.TopK
	}
	if req.RepetitionPenalty != nil {
		merge["repetition_penalty"] = *req.RepetitionPenalty
	}
	if req.EnableSearch != nil {
		merge["enable_search"] = *req.EnableSearch
	}
	if req.SearchOptions != nil {
		merge["search_options"] = req.SearchOptions
	}
	if req.EnableThinking != nil {
		merge["enable_thinking"] = *req.EnableThinking
	}
	if req.PreserveThinking != nil {
		merge["preserve_thinking"] = *req.PreserveThinking
	}
	if req.ThinkingBudget != nil {
		merge["thinking_budget"] = *req.ThinkingBudget
	}
	if req.EnableCodeInterpreter != nil {
		merge["enable_code_interpreter"] = *req.EnableCodeInterpreter
	}
	if req.VLHighResolutionImages != nil {
		merge["vl_high_resolution_images"] = *req.VLHighResolutionImages
	}
	for k, v := range merge {
		baseReq[k] = v
	}

	return json.Marshal(baseReq)
}
