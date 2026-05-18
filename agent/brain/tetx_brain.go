package brain

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// TextBrain 实现了文本处理的 Brain 接口
type TextBrain struct {
	config *Config
	client *http.Client
}

// NewTextBrain 创建新的文本处理大脑。
//
// 这里**不**用 http.Client.Timeout：它是"整个请求"的硬上限，对流式响应来说
// 就是"读流体的总时长"，长输出（写小说、大 JSON 萃取）会被无情切断。
// 取而代之，我们用 Transport.ResponseHeaderTimeout 控制握手阶段，body 读取
// 完全交由调用方的 ctx 取消——这才是 Go 流式 HTTP 的正确做法。
func NewTextBrain(config *QwenConfig) *TextBrain {
	transport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second, // 连接 + 握手 + 等响应头：30s 上限
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
	}
	return &TextBrain{
		config: config,
		client: &http.Client{
			Transport: transport,
			// Timeout: 0，没有整体 deadline，靠 ctx 控制
		},
	}
}

// ProcessInput 实现 Brain 接口
func (tb *TextBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return brainError("input is nil"), fmt.Errorf("input is nil")
	}

	req := &ChatCompletionRequest{
		Model:       input.Options.Model,
		Messages:    input.Messages,
		Stream:      input.Options.Stream,
		Tools:       input.Tools,
		Temperature: input.Options.Temperature,
		TopP:        input.Options.TopP,
		MaxTokens:   input.Options.MaxTokens,
	}

	if req.Model == "" {
		req.Model = tb.config.Model
	}

	// 透传 Extra 到 extra_body / 顶层扩展字段：enable_thinking、thinking_budget、enable_search 等
	if len(input.Extra) > 0 {
		req.ExtraBody = input.Extra
	}

	// 调用千问模型
	ctx := input.ContextOrBackground()
	if input.Options.Stream {
		return tb.CreateChatCompletionStream(ctx, *req)
	}
	response, err := tb.CreateChatCompletion(ctx, *req)

	if err != nil {
		return brainError(err.Error()), err
	}

	if len(response.Choices) == 0 {
		return brainError("no response from model"), fmt.Errorf("no response from model")
	}

	content, _ := response.Choices[0].Message.Content.(string)
	if content == "" && len(response.Choices[0].Message.ToolCalls) == 0 {
		return brainError("empty response content"), fmt.Errorf("empty response content")
	}

	out := brainSuccess(BrainModeText)
	out.Content.Text = content
	out.Content.Messages = []Message{
		{
			Role:      "assistant",
			Content:   content,
			ToolCalls: response.Choices[0].Message.ToolCalls,
		},
	}
	out.Choices = response.Choices
	out.Usage = &Usage{
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		TotalTokens:      response.Usage.TotalTokens,
	}
	return &out, nil
}

// CreateChatCompletion 创建聊天完成 (非流式)
func (tb *TextBrain) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return createChatCompletion(ctx, tb.client, tb.config, req)
}

// 创建流式聊天完成
func (tb *TextBrain) CreateChatCompletionStream(ctx context.Context, req ChatCompletionRequest) (*BrainOutput, error) {
	out, err := createChatCompletionStream(ctx, tb.client, tb.config, req)
	if err != nil {
		return nil, err
	}
	out.Mode = BrainModeText
	return out, nil
}

// GetBaseURL 获取当前配置的基础URL (公开方法)
func (tb *TextBrain) GetBaseURL() string {
	return getChatCompletionsURL(tb.config)
}
