# FullModel SDK Guide

这份文档专门给 Go 应用开发者看：引入 `github.com/xumi30/fullmodel` 后，如何用最少代码接入文本、流式、会话记忆、多模态、生成能力和工具调用。

## 安装

```bash
go get github.com/xumi30/fullmodel
```

准备配置文件 `config/llm.yaml`：

```yaml
defaults:
  profile: qwen

profiles:
  qwen:
    provider: qwen
    api_key_env: DASHSCOPE_API_KEY

brains:
  text:
    model: qwen-plus
  image:
    model: qwen-vl-plus
  speech_to_text:
    model: qwen-audio-asr
  text_to_speech:
    model: qwen-tts
  image_generate:
    model: qwen-image-2.0-pro
```

```bash
export DASHSCOPE_API_KEY="your-api-key"
```

## 最小可用

```go
package main

import (
	"context"
	"fmt"

	"github.com/xumi30/fullmodel"
)

func main() {
	client, err := fullmodel.Open()
	if err != nil {
		panic(err)
	}

	text, err := client.Text(context.Background(), "写一句欢迎语")
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
}
```

## Client 创建

```go
client, err := fullmodel.Open()
```

指定配置文件：

```go
client, err := fullmodel.Open(
	fullmodel.WithConfigFile("./config/llm.yaml"),
)
```

直接传入已解析配置：

```go
client, err := fullmodel.Open(
	fullmodel.WithConfigs(configs),
)
```

示例或脚本里可以用：

```go
client := fullmodel.MustOpen()
```

## 文本

```go
text, err := client.Text(ctx, "写一句欢迎语")
```

带会话记忆：

```go
reply, err := client.Chat(ctx, "user-42", "我叫 Lei，记住这个名字")
reply, err = client.Chat(ctx, "user-42", "我叫什么？")
```

底层统一入口：

```go
result, err := client.Run(ctx, processmessage.TextMessage{
	Text: "总结一下 FullModel 的能力",
})
```

## 流式文本

`StreamText` 不会帮你收集分片，而是返回流对象，你自己消费通道：

```go
stream, err := client.StreamText(ctx, "写一句欢迎语")
if err != nil {
	return err
}

for chunk := range stream.Text() {
	fmt.Print(chunk)
}

if err := stream.Wait(); err != nil {
	return err
}
```

别名：

```go
stream, err := client.TextStream(ctx, "写一句欢迎语")
```

流对象接口：

```go
type StreamOutput interface {
	Text() <-chan string
	ToolCalls() <-chan []brain.ToolCall
	Error() <-chan error
	Cancel()
	Wait() error
}
```

`StreamText` 默认不带 runtime 默认工具，适合“只要文本分片”的场景。需要工具调用时，使用非流式 `Text`/`Chat` 的 Tool Loop，或显式传入自己的 `processmessage.Options.Tools` 后自行消费 `stream.ToolCalls()`。

## 记忆管理

默认 `client.Chat(ctx, sessionID, text)` 会自动读取 session 历史，并把用户输入和助手回复写回。

手动管理：

```go
memory := client.Memory()

memory.RememberSystem("user-42", "你是一个简洁的助手")
memory.RememberUser("user-42", "我喜欢 Go")
memory.RememberAssistant("user-42", "记住了")

history := memory.Messages("user-42")
memory.Replace("user-42", history[:1])
memory.Clear("user-42")
```

清空某个 session：

```go
client.ClearSession("user-42")
```

生产环境替换为文件存储：

```go
import agentruntime "github.com/xumi30/fullmodel/agent/runtime"

store, err := agentruntime.NewFileSessionStore("./data/sessions")
if err != nil {
	return err
}

client, err := fullmodel.Open(
	fullmodel.WithSessionMemory(store),
)
```

也可以自己实现：

```go
type SessionMemory interface {
	Messages(sessionID string) []brain.Message
	Append(sessionID string, messages ...brain.Message)
	Replace(sessionID string, messages []brain.Message)
	Clear(sessionID string)
}
```

## Tool 调用

FullModel 的 SDK 支持自动 Tool Loop：模型如果返回 tool call，runtime 会执行工具，把 tool result 回灌给模型，再返回最终回答。

### 自定义工具

```go
type WeatherArgs struct {
	City string `json:"city"`
}

weatherTool := fullmodel.NewTool(
	"get_weather",
	"查询城市天气",
	fullmodel.ObjectSchema(map[string]any{
		"city": map[string]any{
			"type":        "string",
			"description": "城市名，例如 Hangzhou",
		},
	}, "city"),
	func(ctx context.Context, raw string) (string, error) {
		var args WeatherArgs
		if err := fullmodel.DecodeToolArguments(raw, &args); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s 今天晴，24°C", args.City), nil
	},
)

client, err := fullmodel.Open(
	fullmodel.WithTools(weatherTool),
)

answer, err := client.Chat(ctx, "user-42", "杭州今天天气怎么样？")
```

### 查看工具列表

```go
tools := client.Tools()
for _, tool := range tools {
	fmt.Println(tool.Function.Name, tool.Function.Description)
}
```

### 手动执行某个工具

适合调试工具 handler：

```go
result, err := client.ExecuteTool(ctx, brain.ToolCall{
	Function: brain.FunctionCall{
		Name:      "get_weather",
		Arguments: `{"city":"Hangzhou"}`,
	},
})
```

### 完全自定义 ToolExecutor

如果你已有自己的工具系统，实现这个接口即可：

```go
type ToolExecutor interface {
	Tools() []brain.Tool
	Execute(ctx context.Context, call brain.ToolCall) (string, error)
}
```

```go
client, err := fullmodel.Open(
	fullmodel.WithToolExecutor(myExecutor),
)
```

## 日志与验证

默认日志文件：

```bash
logs/default.log
```

实时查看：

```bash
tail -f logs/default.log
```

应用侧可以直接引入日志包：

```go
import "github.com/xumi30/fullmodel/utils/logging"

logging.Info("user message session=%s text=%q", sessionID, text)
logging.Debug("tool result=%s", result)
logging.Warn("stream retry attempt=%d", attempt)
logging.Error("stream failed: %v", err)
```

创建自己的 logger：

```go
logger := logging.NewLogger("my-app", "logs/my-app.log", logging.INFO, 20*1024*1024)
defer logger.Close()

logger.Info("boot")
logger.Error("failed: %v", err)
```

替换默认 logger：

```go
logger := logging.NewLogger("default", "logs/app-default.log", logging.DEBUG, 20*1024*1024)
logging.SetDefaultLogger(logger)
defer logging.CloseAll()
```

日志级别：

```go
logging.DEBUG
logging.INFO
logging.WARN
logging.ERROR
```

流式文本符合预期时，会看到：

```text
sdk StreamText stream ready ...
chat stream create ... tools=0
chat stream emitted text ...
chat stream done ... text_chunks=...
stream complete final_error=<nil>
```

Tool Loop 符合预期时，会看到第一轮带工具，第二轮带 tool message 回灌：

```text
[llm.request] nonstream ... tools=1
[llm.response] nonstream decoded ... contentRunes=0
[llm.request] nonstream ... messages=3 tools=1
```

可以直接跑真实 SDK 示例验证：

```bash
go run ./examples/sdk_interfaces
```

默认会覆盖 `Text`、`Run`、`StreamText`、`TextStream`、`Chat`、`Memory`、`ToolLoop`、`ExecuteTool`。媒体和生成能力默认不跑，需要显式开启：

```bash
FULLMODEL_EXAMPLE_MEDIA=1 go run ./examples/sdk_interfaces
```

## 多模态理解

图片理解：

```go
image := fullmodel.MediaFromURL("https://example.com/cat.png")
text, err := client.Image(ctx, image, "这张图里有什么？")
```

本地图片：

```go
image, err := fullmodel.MediaFromFile("./cat.png")
text, err := client.Image(ctx, image, "描述这张图")
```

视频理解：

```go
video := fullmodel.MediaFromURL("https://example.com/demo.mp4")
text, err := client.Video(ctx, video, "总结这个视频")
```

## 语音

语音转文字：

```go
audio, err := fullmodel.MediaFromFile("./speech.wav")
text, err := client.ASR(ctx, audio)
```

文字转语音：

```go
audioBytes, err := client.TTS(ctx, "你好，欢迎使用 FullModel")
```

## 生成能力

文生图：

```go
url, err := client.GenerateImage(ctx, "一张极简风格的产品海报")
```

图像编辑：

```go
image, err := fullmodel.MediaFromFile("./poster.png")
url, err := client.EditImage(ctx, image, "把背景改成深色科技风")
```

文生视频：

```go
url, err := client.TextToVideo(ctx, "一只小机器人在工作台上整理工具")
```

图生视频：

```go
frame, err := fullmodel.MediaFromFile("./first-frame.png")
url, err := client.ImageToVideo(ctx, frame, "镜头缓慢推进，灯光变亮")
```

## RunOption

指定 session：

```go
text, err := client.Text(ctx, "继续", fullmodel.WithSession("user-42"))
```

强制流式：

```go
result, err := client.Run(ctx, msg, fullmodel.WithStream(true))
```

传入 process options：

```go
text, err := client.Text(ctx, "写一句欢迎语",
	fullmodel.WithProcessOptions(processmessage.Options{
		Model:       "qwen-plus",
		Temperature: 0.7,
		MaxTokens:   512,
	}),
)
```

## 接口速查

```go
fullmodel.Open(opts ...Option) (*Client, error)
fullmodel.MustOpen(opts ...Option) *Client

client.Run(ctx, message, opts...) (*runtime.Result, error)
client.Text(ctx, text, opts...) (string, error)
client.StreamText(ctx, text, opts...) (brain.StreamOutput, error)
client.TextStream(ctx, text, opts...) (brain.StreamOutput, error)
client.Chat(ctx, sessionID, text, opts...) (string, error)

client.Image(ctx, image, prompt, opts...) (string, error)
client.Video(ctx, video, prompt, opts...) (string, error)
client.GenerateImage(ctx, prompt, opts...) (string, error)
client.EditImage(ctx, image, prompt, opts...) (string, error)
client.TextToVideo(ctx, prompt, opts...) (string, error)
client.ImageToVideo(ctx, firstFrame, prompt, opts...) (string, error)
client.ASR(ctx, audio, opts...) (string, error)
client.TTS(ctx, text, opts...) ([]byte, error)

client.Memory() *fullmodel.Memory
client.ClearSession(sessionID)

client.Tools() []brain.Tool
client.ExecuteTool(ctx, call) (string, error)

fullmodel.MediaFromURL(url) brain.MediaResource
fullmodel.MediaFromFile(path) (brain.MediaResource, error)
fullmodel.MustMediaFromFile(path) brain.MediaResource
fullmodel.DetectMime(path, data) string

fullmodel.NewTool(name, description, parameters, handler) SDKTool
fullmodel.NewToolSet(tools...) *ToolSet
fullmodel.DecodeToolArguments(arguments, &v) error
fullmodel.ObjectSchema(properties, required...) map[string]any
```
