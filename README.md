# FullModel

FullModel 是一个 Go 写的多模态 Agent Runtime。它把文本、图片、视频、语音、图像生成、视频生成、工具调用和流式响应收进同一套运行时接口里，让你引入这个库后就能很快把 AI 能力开放给自己的应用。

核心目标很简单：应用层只关心“收到什么消息、返回什么结果”，不用到处手动拼模型请求。

SDK 详细用法见 [SDK.md](./SDK.md)，里面包含文本、流式、记忆、多模态、生成能力和 Tool 调用示例。

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

### 2. 日志与排查

FullModel 默认会把运行日志写到：

```bash
logs/default.log
```

实时查看：

```bash
tail -f logs/default.log
```

应用也可以直接使用 FullModel 的日志接口：

```go
import "github.com/xumi30/fullmodel/utils/logging"

func main() {
	logging.Info("server started addr=%s", "127.0.0.1:8080")
	logging.Warn("retrying request id=%s", "req-1")
	logging.Error("request failed: %v", err)
}
```

需要独立日志文件时：

```go
logger := logging.NewLogger("app", "logs/app.log", logging.INFO, 10*1024*1024)
logger.Info("application booted")
defer logger.Close()
```

常用接口：

```go
logging.Debug(format, args...)
logging.Info(format, args...)
logging.Warn(format, args...)
logging.Error(format, args...)
logging.NewLogger(name, filePath, level, maxSize)
logging.GetLogger(name)
logging.SetDefaultLogger(logger)
logging.CloseAll()
```

流式请求重点看这些日志：

```text
sdk StreamText stream ready ...
chat stream create ... tools=0
chat stream response status=200 ...
chat stream emitted text ...
chat stream done ... text_chunks=...
stream complete final_error=<nil>
```

Tool Loop 重点看：

```text
[llm.request] nonstream ... tools=1
[llm.response] nonstream decoded ... contentRunes=0
[llm.request] nonstream ... messages=3 tools=1
```

`contentRunes=0` 通常表示模型第一轮返回的是 tool call，runtime 会执行工具并发起第二轮请求生成最终回答。

SDK 接口真实检查示例：

```bash
go run ./examples/sdk_interfaces
```

默认会真实调用文本、流式、会话记忆和 Tool Loop，并在终端输出 `[PASS]`。如果还想跑 TTS、图像生成、视频生成：

```bash
FULLMODEL_EXAMPLE_MEDIA=1 go run ./examples/sdk_interfaces
```

TTS 音色由模型决定，不能把不同模型版本的音色混用。当前示例配置使用 `cosyvoice-v3-flash`，应选择 v3/flash 支持的音色，例如：

```text
longanyang        龙安洋，阳光大男孩，普通话/英文
longanhuan        龙安欢，欢脱元气女，普通话/英文
longhuhu_v3       龙呼呼，女童声
longpaopao_v3     龙泡泡，儿童/故事机
longjielidou_v3   龙杰力豆，男童声
longxian_v3       龙仙，豪放可爱女
longling_v3       龙铃，稚气女声
longshanshan_v3   龙闪闪，戏剧化童声
longniuniu_v3     龙牛牛，男童声
longjiaxin_v3     粤语女
longjiayi_v3      粤语女
longanyue_v3      粤语男
longlaotie_v3     东北话男
longshange_v3     陕北男
longanmin_v3      闽南话女
loongkyong_v3     韩语女
loongriko_v3      日语女
loongtomoka_v3    日语女
longfei_v3        诗词朗诵/磁性男
longyingxiao_v3   电话销售女
longyingxun_v3    客服男
longyingjing_v3   冷静女
longyingling_v3   共情女
longyingtao_v3    温柔女
longxiaochun_v3   知性积极女
longxiaoxia_v3    沉稳权威女
longyumi_v3       YUMI，年轻女声
```

完整列表以阿里云官方文档为准：[CosyVoice 音色列表](https://www.alibabacloud.com/help/zh/model-studio/cosyvoice-voice-list)。

### 3. 在 Go 应用里嵌入

```go
package main

import (
	"context"
	"fmt"

	"github.com/xumi30/fullmodel"
	agentruntime "github.com/xumi30/fullmodel/agent/runtime"
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

常见能力也有快捷方法：

```go
text, err := client.Text(ctx, "写一句欢迎语")
reply, err := client.Chat(ctx, "session-1", "继续刚才的话题")
client.ClearSession("session-1")

image := fullmodel.MediaFromURL("https://example.com/cat.png")
caption, err := client.Image(ctx, image, "这张图里有什么？")

audio := fullmodel.MustMediaFromFile("./hello.wav")
transcript, err := client.ASR(ctx, audio)

speech, err := client.TTS(ctx, "你好，欢迎使用 FullModel")
speech, err = client.TTS(ctx, "你好，欢迎使用 FullModel",
	fullmodel.WithTTSVoice("longxiaochun_v3"),
	fullmodel.WithTTSFormat("mp3"),
	fullmodel.WithTTSSampleRate(22050),
)
imageURL, err := client.GenerateImage(ctx, "一张极简风格的产品海报")
videoURL, err := client.TextToVideo(ctx, "一只小机器人在工作台上整理工具")
```

媒体输入可以来自 URL 或本地文件：

```go
img := fullmodel.MediaFromURL("https://example.com/a.png")
wav, err := fullmodel.MediaFromFile("./speech.wav")
```

如果你想自己消费流式分片，可以使用底层流接口：

```go
stream, err := client.TextStream(ctx, "写一句欢迎语")
```

或者直接使用更顺口的流式快捷方法：

```go
stream, err := client.StreamText(ctx, "写一句欢迎语")
for chunk := range stream.Text() {
	fmt.Print(chunk)
}
if err := stream.Wait(); err != nil {
	return err
}
```

需要流式会话记忆时，用 `StreamChat`：

```go
stream, err := client.StreamChat(ctx, "user-42", "继续刚才的话题")
for chunk := range stream.Text() {
	fmt.Print(chunk)
}
if err := stream.Wait(); err != nil {
	return err
}
```

### SDK 记忆管理

`client.Chat(ctx, sessionID, text)` 和 `client.StreamChat(ctx, sessionID, text)` 会自动读取并写回该 session 的对话历史：

```go
reply, err := client.Chat(ctx, "user-42", "我叫 Lei，记住这个名字")
reply, err = client.Chat(ctx, "user-42", "我叫什么？")
```

`Text` / `StreamText` 默认是单次对话；如果传入 `fullmodel.WithSession("user-42")` 才会读取 session 历史。`Text + WithSession` 会记住 user 和 assistant；`StreamText + WithSession` 只会自动记住 user，assistant 的流式内容需要你自己收集后写入 memory。想要自动记完整流式回复，直接用 `StreamChat`。

需要手动管理历史时，用 `client.Memory()`：

```go
memory := client.Memory()

memory.RememberSystem("user-42", "你是一个简洁的助手")
memory.RememberUser("user-42", "我喜欢 Go")
memory.RememberAssistant("user-42", "记住了")

history := memory.Messages("user-42")
memory.Replace("user-42", history[:1])
memory.Clear("user-42")
```

生产环境可以替换存储：

```go
store, err := agentruntime.NewFileSessionStore("./data/sessions")
client, err := fullmodel.Open(fullmodel.WithSessionMemory(store))
```

### 4. 开放 HTTP API

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

## 示例

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/xumi30/fullmodel"
	"github.com/xumi30/fullmodel/agent/brain"
)

func main() {
	ctx := context.Background()

	client, err := fullmodel.Open()
	if err != nil {
		log.Fatal(err)
	}

	app := NewAssistant(client, "demo-session-1", `你是一个资深 Go 助手。回答要简洁、结构化，优先给可执行步骤。`)

	mustAsk(ctx, app, "我想做一个带重试的 HTTP 客户端，先给设计思路。")
	mustAsk(ctx, app, "给我一个最小可运行的代码骨架。")
}

type Assistant struct {
	client    *fullmodel.Client
	sessionID string
}

func NewAssistant(client *fullmodel.Client, sessionID, systemPrompt string) *Assistant {
	client.Memory().Replace(sessionID, []brain.Message{
		brain.NewSystemMessage(systemPrompt),
	})
	return &Assistant{client: client, sessionID: sessionID}
}

func (a *Assistant) Ask(ctx context.Context, userPrompt string) (string, error) {
	return a.client.Chat(ctx, a.sessionID, userPrompt)
}

func mustAsk(ctx context.Context, app *Assistant, userPrompt string) {
	reply, err := app.Ask(ctx, userPrompt)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("user:", userPrompt)
	fmt.Println("assistant:", reply)
}
```
