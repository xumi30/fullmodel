package main

import (
	"context"
	"encoding/json"
	"fmt"
	"fullmodel/agent/brain"
	"log"
	"os"
)

func main() {
	// 配置示例
	config := &brain.QwenConfig{
		APIKey:  os.Getenv("DASHSCOPE_API_KEY"),
		Model:   "qwen3.5-plus-2026-04-20",
		Region:  "cn-beijing",
		BaseURL: "", // 使用默认 URL
	}

	// 创建文本大脑
	tb := brain.NewTextBrain(config)
	// mmb := brain.NewMultiModeBrain()

	// // 示例 1: 简单文本对话
	// fmt.Println("=== 简单文本对话示例 ===")
	// err := exampleSimpleText(tb)
	// if err != nil {
	// 	log.Printf("Simple text example error: %v", err)
	// }

	// // 示例 2: 完整 API 调用
	// fmt.Println("\n=== 完整 API 调用示例 ===")
	// err = exampleFullAPI(tb)
	// if err != nil {
	// 	log.Printf("Full API example error: %v", err)
	// }

	// 示例 3: 流式响应（暂时跳过，该模型可能不支持流式）
	fmt.Println("\n=== 流式响应示例 ===")
	err2 := exampleStreaming(tb)
	if err2 != nil {
		log.Printf("Streaming example error: %v", err2)
	}

	// // 示例 4: 多模态输入
	// fmt.Println("\n=== 多模态输入示例 ===")
	// err = exampleMultimodal(mmb)
	// if err != nil {
	// 	log.Printf("Multimodal example error: %v", err)
	// }

	// // 示例 5: 工具调用
	// fmt.Println("\n=== 工具调用示例 ===")
	// err = exampleToolCalling(tb)
	// if err != nil {
	// 	log.Printf("Tool calling example error: %v", err)
	// }
}

func exampleSimpleText(tb *brain.TextBrain) error {
	// 构建 BrainInput
	req := &brain.BrainInput{
		Context: context.Background(),
		Mode:    brain.BrainModeText,
		Messages: []brain.Message{
			{
				Role:    "user",
				Content: "你好，介绍一下自己",
			},
		},
		Stream: false,
	}

	result, err := tb.ProcessInput(req)
	if err != nil {
		return err
	}
	fmt.Printf("Response: %+v\n", result)
	return nil
}

func exampleFullAPI(tb *brain.TextBrain) error {
	ctx := context.Background()

	messages := []brain.Message{
		brain.NewSystemMessage("你是一个有用的助手"),
		brain.NewUserMessage("用中文解释一下人工智能"),
	}

	req := brain.ChatCompletionRequest{
		Model:       "qwen-max",
		Messages:    messages,
		Temperature: floatPtr(0.7),
		MaxTokens:   intPtr(500),
		Stream:      false,
	}

	resp, err := tb.CreateChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	fmt.Printf("Model: %s\n", resp.Model)
	fmt.Printf("Usage: %+v\n", resp.Usage)

	if len(resp.Choices) > 0 {
		content := resp.Choices[0].Message.Content
		if str, ok := content.(string); ok {
			fmt.Printf("Content: %s\n", str)
		} else {
			fmt.Printf("Content type: %T\n", content)
		}
	}

	return nil
}

func exampleStreaming(tb *brain.TextBrain) error {
	ctx := context.Background()

	messages := []brain.Message{
		brain.NewUserMessage("说个冷笑话"),
	}

	req := brain.ChatCompletionRequest{
		Model:          "qwen3.5-plus-2026-04-20",
		Messages:       messages,
		Stream:         true,
		EnableThinking: new(false), // 启用思考过程展示
	}

	result, err := tb.CreateChatCompletionStream(ctx, req)
	if err != nil {
		fmt.Printf("CreateChatCompletionStream error: %v\n", err)
	}
	if result.Usage.TotalTokens != 0 {
		fmt.Printf("Total tokens: %d\n", result.Usage.TotalTokens)
	}

	fmt.Print("Streaming response: ")
	for {
		select {
		case chunk, _ := <-result.TextStream:
			fmt.Print(chunk)

		case err, ok := <-result.ErrorStream:
			if !ok {
				// 错误通道已关闭，没有错误发生
				return nil
			}
			if err != nil {
				// 确实发生了错误
				return err
			}
		}
	}
}

func exampleToolCalling(tb *brain.TextBrain) error {
	ctx := context.Background()

	// 定义工具
	tools := []brain.Tool{
		{
			Type: "function",
			Function: brain.FunctionDefinition{
				Name:        "get_weather",
				Description: "获取城市天气信息",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{
							"type":        "string",
							"description": "城市名称",
						},
					},
					"required": []string{"city"},
				},
			},
		},
	}

	messages := []brain.Message{
		brain.NewUserMessage("北京今天的天气怎么样？"),
	}

	req := brain.ChatCompletionRequest{
		Model:    "qwen-max",
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}

	resp, err := tb.CreateChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	// 处理工具调用响应
	if len(resp.Choices) > 0 && resp.Choices[0].FinishReason == "tool_calls" {
		fmt.Println("模型请求调用工具:")

		// 这里可以调用实际工具并继续对话
		messageJSON, _ := json.MarshalIndent(resp.Choices[0].Message, "", "  ")
		fmt.Printf("工具调用消息: %s\n", string(messageJSON))
	}

	return nil
}

// 辅助函数
func floatPtr(f float64) *float64 {
	return &f
}

func intPtr(i int) *int {
	return &i
}
