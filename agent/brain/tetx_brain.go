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

// NewTextBrain 创建新的文本处理大脑
func NewTextBrain(config *QwenConfig) *TextBrain {
	return &TextBrain{
		config: config,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ProcessInput 实现 Brain 接口
func (tb *TextBrain) ProcessInput(input *BrainInput) (*BrainOutput, error) {
	if input == nil {
		return &BrainOutput{
			Success: false,
			Error:   "input is nil",
		}, fmt.Errorf("input is nil")
	}

	// 将 BrainInput 转换为 ChatCompletionRequest
	req := &ChatCompletionRequest{
		Model:    input.Model,
		Messages: input.Messages,
		Stream:   input.Stream,
		Tools:    input.Tools,
	}

	// 如果传入的请求没有模型信息，使用配置中的模型
	if req.Model == "" {
		req.Model = tb.config.Model
	}

	// 调用千问模型
	if input.Stream {
		return tb.CreateChatCompletionStream(input.Context, *req)
	}
	response, err := tb.CreateChatCompletion(input.Context, *req)

	if err != nil {
		return &BrainOutput{
			Success: false,
			Error:   err.Error(),
		}, err
	}

	if len(response.Choices) == 0 {
		return &BrainOutput{
			Success: false,
			Error:   "no response from model",
		}, fmt.Errorf("no response from model")
	}

	content, _ := response.Choices[0].Message.Content.(string)
	if content == "" && len(response.Choices[0].Message.ToolCalls) == 0 {
		return &BrainOutput{
			Success: false,
			Error:   "empty response content",
		}, fmt.Errorf("empty response content")
	}

	return &BrainOutput{
		Success: true,
		Text:    content,
		Mode:    BrainModeText,
		Choices: response.Choices,
		Messages: []Message{
			{
				Role:      "assistant",
				Content:   content,
				ToolCalls: response.Choices[0].Message.ToolCalls,
			},
		},
		Usage: &Usage{
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
			TotalTokens:      response.Usage.TotalTokens,
		},
	}, nil
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
