# FullModel - AI Agent Framework

FullModel 是一个基于 Go 语言构建的智能体（Agent）框架，支持多模态AI模型调用、工具调用和流式响应等功能。

## 特性

- 🤖 **智能体架构**：完整的 Agent、Brain、Memory、Tools 分层设计
- 🔄 **多模态支持**：文本、图像、音频、视频等多种模态处理
- 🛠️ **工具调用**：支持函数调用和外部工具集成
- ⚡ **流式响应**：实时响应输出，支持思考过程展示
- 📚 **记忆机制**：支持对话历史管理和上下文维护
- 🎯 **易于扩展**：模块化设计，便于添加新的模型和功能

## 项目结构

```
fullmodel/
├── agent/           # 智能体核心模块
│   ├── brain/      # 大脑模块（模型调用）
│   ├── memory/     # 记忆模块
│   └── tools/      # 工具模块
├── examples/        # 使用示例
│   ├── agenttest/
│   ├── openai_*/   # OpenAI 各种功能示例
│   └── example.go  # 主示例文件
└── utils/          # 工具函数
    ├── fileop/
    ├── logging/
    └── systemprompt.go
```

## 快速开始

### 环境要求

- Go 1.26.1 或更高版本
- 支持的AI模型API密钥（如 DashScope API Key）

### 安装

```bash
git clone https://github.com/your-username/fullmodel.git
cd fullmodel
go mod tidy
```

### 基本使用

```go
package main

import (
    "context"
    "fullmodel/agent/brain"
    "os"
)

func main() {
    // 配置模型
    config := &brain.QwenConfig{
        APIKey: os.Getenv("DASHSCOPE_API_KEY"),
        Model:  "qwen3.5-plus-2026-04-20",
        Region: "cn-beijing",
    }

    // 创建文本大脑
    tb := brain.NewTextBrain(config)

    // 创建会话请求
    req := &brain.BrainInput{
        Context: context.Background(),
        Mode:    brain.BrainModeText,
        Messages: []brain.Message{{
            Role:    "user",
            Content: "你好，介绍一下自己",
        }},
    }

    // 获取响应
    result, err := tb.ProcessInput(req)
    if err != nil {
        panic(err)
    }
    
    // 处理结果
    fmt.Printf("Response: %+v\n", result)
}
```

## 示例

项目包含丰富的使用示例，位于 `examples/` 目录：

- **基础对话**：简单文本交互示例
- **完整API调用**：带参数配置的完整请求
- **流式响应**：实时输出展示
- **工具调用**：函数调用功能演示
- **多模态处理**：图像、语音、视频等场景

运行示例：
```bash
cd examples/
go run example.go
```

## 模型支持

目前支持的模型：

- **通义千问系列**：qwen-max, qwen3.5-plus 等
- **OpenAI兼容API**：支持标准的ChatCompletion接口

## 配置

### 环境变量

```bash
export DASHSCOPE_API_KEY="your-api-key"
```

### 模型配置

```go
config := &brain.QwenConfig{
    APIKey:  "your-api-key",
    Model:   "qwen3.5-plus-2026-04-20",
    Region:  "cn-beijing",
    BaseURL: "", // 可选，自定义API地址
}
```

## 高级功能

### 工具调用

定义和使用工具：

```go
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
```

### 流式响应

启用流式输出：

```go
req := brain.ChatCompletionRequest{
    Model:    "qwen3.5-plus-2026-04-20",
    Messages: messages,
    Stream:   true,
    EnableThinking: new(false), // 启用思考过程
}
```

## 开发

### 添加新的模型支持

1. 在 `agent/brain/` 下创建新的brain实现
2. 实现 `Brain` 接口方法
3. 添加相应的配置结构

### 扩展工具功能

1. 在 `agent/tools/` 下创建新工具
2. 实现工具的具体逻辑
3. 注册到工具库中

## 贡献

欢迎提交 Issue 和 Pull Request！

## 许可证

MIT License

## 联系方式

- 项目主页：[GitHub Repository]
- 问题反馈：[Issues]
- 文档：[Wiki]