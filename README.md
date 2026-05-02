# FullModel

FullModel 是一个 Go 写的多模态 Agent Runtime。它把文本、图片、视频、语音、图像生成、视频生成、工具调用和流式响应收进同一套运行时接口里，让你引入这个库后就能很快把 AI 能力开放给自己的应用。

核心目标很简单：应用层只关心“收到什么消息、返回什么结果”，不用到处手动拼模型请求。

## 你可以用它做什么

- 在 Go 应用里直接嵌入多模态 Agent 能力
- 快速开放 HTTP API 给前端、桌面端、机器人或其他服务
- 用 CLI 调试 text、chat、image、video、asr、tts、image generate、video generate
- 接入工具调用，让模型自动调用本地函数并回灌结果
- 用统一消息模型处理不同来源：CLI、HTTP、Webhook、IM、桌面应用

## 架构

```text
Application
  -> processmessage.Message
  -> runtime.Runner
  -> runtime.Registry
  -> brain.Brain
  -> brain.BrainOutput
```

主要包：

```text
agent/brain        模型能力封装，负责真实 API 调用
agent/runtime      应用运行时：能力注册、Runner、Tool Loop、Session
processmessage     统一消息模型，把外部输入转成 BrainInput
cmd/chat           交互式 CLI 调试入口
cmd/fullmodel      HTTP/API 服务入口
utils/fileop       配置读取
```

## 快速开始

### 1. 配置模型

创建 `config/llm.yaml`：

```yaml
defaults:
  profile: qwen

profiles:
  qwen:
    api_key: ${DASHSCOPE_API_KEY}
    provider: qwen
    region: cn-beijing

brains:
  text:
    model: qwen-plus
  vision:
    model: qwen-vl-plus
  voice:
    model: cosyvoice-v3-flash
  image:
    model: qwen-image-2.0-pro
```

设置环境变量：

```bash
export DASHSCOPE_API_KEY="your-api-key"
```

`profiles` 放供应商/API Key/BaseURL 这类通用配置，`brains.<name>` 只写每个能力自己的差异。

### 2. 在 Go 应用里嵌入

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

	text, err := client.Text(context.Background(), "你好，介绍一下你自己")
	if err != nil {
		panic(err)
	}

	fmt.Println(text)
}
```

需要更细粒度控制时，也可以继续使用 `processmessage.Message` 和 `runtime.Runner`：

```go
result, err := client.Run(ctx, processmessage.ImageGenerateMessage{
	Prompt: "一张极简风格的产品海报",
})
```

### 3. 开放 HTTP API

直接启动内置服务：

```bash
go run ./cmd/fullmodel serve -addr 127.0.0.1:8080
```

生产或共享环境建议启用 API Key：

```bash
export FULLMODEL_API_KEY="change-me"
go run ./cmd/fullmodel serve -addr 127.0.0.1:8080
```

调用时传入：

```bash
curl -H "Authorization: Bearer $FULLMODEL_API_KEY" http://127.0.0.1:8080/v1/capabilities
```

查看能力：

```bash
curl -s http://127.0.0.1:8080/v1/capabilities
```

调用文本能力：

```bash
curl -s http://127.0.0.1:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{
    "kind": "text",
    "text": "给我一个三句话的产品介绍"
  }'
```

带会话的聊天：

```bash
curl -s http://127.0.0.1:8080/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "session_id": "demo",
    "text": "我叫 Lei，记住这个名字"
  }'
```

流式响应：

```bash
curl -N http://127.0.0.1:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{
    "kind": "text",
    "text": "写一段很短的故事",
    "stream": true
  }'
```

## CLI 调试

交互式调试：

```bash
go run ./cmd/chat
```

常用命令：

```text
text <prompt>                     单轮文本
stream <prompt>                   流式文本
chat <message>                    多轮对话
image <file-or-url> | <prompt>    图片理解
video <file-or-url> | <prompt>    视频理解
asr <audio-file>                  语音识别
tts <text> [> output.mp3]         文本转语音
imagine <prompt>                  文生图
edit <image> | <prompt>           图像编辑
t2v <prompt>                      文生视频
i2v <image> | <prompt>            图生视频
```

单次命令调用：

```bash
go run ./cmd/fullmodel run -kind text -prompt "你好"
```

## 统一消息模型

应用层推荐只使用 `processmessage` 里的消息类型：

```go
processmessage.TextMessage
processmessage.ChatMessage
processmessage.ImageMessage
processmessage.VideoMessage
processmessage.SpeechToTextMessage
processmessage.TextToSpeechMessage
processmessage.ImageGenerateMessage
processmessage.ImageEditMessage
processmessage.TextToVideoMessage
processmessage.ImageToVideoMessage
processmessage.MultimodalMessage
```

这些消息会被 `runtime.Runner` 规范化，然后自动路由到对应的 brain。

## 工具调用

`runtime.Runner` 支持 Tool Loop。你只需要提供一个 `ToolExecutor`：

```go
type ToolExecutor interface {
	Tools() []brain.Tool
	Execute(ctx context.Context, call brain.ToolCall) (string, error)
}
```

Runner 会在 text/chat 的非流式输出中检测 tool calls，执行工具，把结果作为 tool message 回灌，再继续向模型请求最终回答。

如果你使用项目里的 `agent/tools` 注册器，可以这样适配：

```go
toolExecutor := agentruntime.NewToolRegistryExecutor(tools.Getregistry())
runner := agentruntime.NewRunner(registry, toolExecutor)
```

## HTTP 请求格式

`POST /v1/messages` 使用统一 JSON：

```json
{
  "kind": "image",
  "prompt": "这张图片里有什么？",
  "media": {
    "image": {
      "url": "https://example.com/cat.png"
    }
  }
}
```

响应统一包在 envelope 里：

```json
{
  "id": "req_xxx",
  "status": { "success": true },
  "result": {
    "text": "..."
  }
}
```

### 长任务

图片、音频、视频这类可能耗时的能力可以走任务接口：

```bash
curl -s http://127.0.0.1:8080/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "kind": "image_generate",
    "prompt": "一张极简风格的产品海报"
  }'
```

查询任务：

```bash
curl -s http://127.0.0.1:8080/v1/tasks/<task_id>
```

列出任务：

```bash
curl -s http://127.0.0.1:8080/v1/tasks
```

取消任务：

```bash
curl -X DELETE http://127.0.0.1:8080/v1/tasks/<task_id>
```

任务状态：

```text
queued
running
succeeded
failed
canceled
```

任务由内置 worker queue 执行。启动服务时可以调整 worker 数：

```bash
go run ./cmd/fullmodel serve -task-workers 4
```

### 文件和 Artifacts

HTTP 支持 JSON base64，也支持 `multipart/form-data` 上传：

```bash
curl -s http://127.0.0.1:8080/v1/messages \
  -F kind=image \
  -F prompt=这张图里有什么 \
  -F image=@./cat.png
```

二进制输出会保存为 artifact，并在响应里返回：

```json
{
  "artifacts": [
    {
      "id": "art_xxx",
      "mime_type": "audio/mpeg",
      "path": "...",
      "size": 12345
    }
  ]
}
```

下载 artifact：

```bash
curl -O http://127.0.0.1:8080/v1/artifacts/<artifact_id>
```

列出 artifacts：

```bash
curl -s http://127.0.0.1:8080/v1/artifacts
```

HTTP 服务会把二进制结果写到 `data/artifacts`，维护 `.index.json`，默认 7 天过期并限制单个 artifact 最大 128MB。

### Session 和 Tools

查看会话：

```bash
curl -s http://127.0.0.1:8080/v1/sessions/demo
```

清除会话：

```bash
curl -X DELETE http://127.0.0.1:8080/v1/sessions/demo
```

查看工具：

```bash
curl -s http://127.0.0.1:8080/v1/tools
```

音频/图片也可以传 base64：

```json
{
  "kind": "speech_to_text",
  "media": {
    "audio": {
      "data": "BASE64_AUDIO",
      "mime_type": "audio/wav"
    }
  }
}
```

常用 `kind`：

```text
text
chat
image
video
speech_to_text
text_to_speech
image_generate
image_edit
text_to_video
image_to_video
multimodal
```

## 配置说明

完整配置结构：

```yaml
defaults:
  profile: qwen

profiles:
  qwen:
    api_key: ${DASHSCOPE_API_KEY}
    provider: qwen
    region: cn-beijing
  local:
    api_key: local-key
    provider: openai
    base_url: http://127.0.0.1:11434/v1

brains:
  text:
    model: qwen-plus
  vision:
    model: qwen-vl-plus
  voice:
    model: cosyvoice-v3-flash
  image:
    model: qwen-image-2.0-pro
```

每个 brain 都可以覆盖 `profile`、`api_key`、`provider`、`region`、`base_url`、`model`、`endpoints`。

例如文本走 OpenAI，其他能力走 DashScope：

```yaml
defaults:
  profile: qwen

profiles:
  qwen:
    api_key: ${DASHSCOPE_API_KEY}
    provider: qwen
    region: cn-beijing
  openai:
    api_key: ${OPENAI_API_KEY}
    provider: openai

brains:
  text:
    profile: openai
    model: gpt-4o
  vision:
    model: qwen-vl-plus
  voice:
    model: cosyvoice-v3-flash
  image:
    model: qwen-image-2.0-pro
```

## 扩展自己的应用能力

### 新增一种消息来源

把外部输入转换成 `processmessage.Message`，交给 `Runner`：

```go
msg := processmessage.TextMessage{Text: userInput}
result, err := runner.Run(ctx, agentruntime.Request{Message: msg})
```

### 新增一个模型能力

1. 在 `agent/brain` 实现 `brain.Brain`
2. 在 `runtime.Registry` 注册新的 `processmessage.Kind`
3. 给应用层暴露对应的 `processmessage.Message`

### 替换 Session/Memory

HTTP 服务默认使用 `runtime.FileSessionStore`，会把会话持久化到 `data/sessions`。本地临时场景仍可直接使用 `runtime.SessionStore`。

## 验证

```bash
go test ./...
go run ./cmd/chat
go run ./cmd/fullmodel serve -addr 127.0.0.1:8080
```

## 当前状态

FullModel 现在更像一个应用开放 runtime，而不是单纯 SDK wrapper：

- `fullmodel.Open()` 是应用内嵌的推荐主入口
- `agent/runtime` 是底层运行时主入口
- `processmessage` 是推荐输入边界
- `cmd/fullmodel` 是开箱即用的 HTTP 服务
- `cmd/chat` 是本地调试入口

后续可以继续把观测指标、限流、Webhook 回调、更多 provider adapter 做成可插拔模块。
