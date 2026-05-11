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

创建 `config/llm.yaml`，或复制仓库 [`config/llm.yaml.example`](./config/llm.yaml.example)。

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
  # fullmodel serve：与 WebSocket 实时 TTS 同源；亦为 POST /v1/voice/customizations 在省略 target_model 时的默认目标模型（与 voice 无关）。
  voice_realtime_ws:
    model: qwen3-tts-vc-realtime-2026-01-15
  image:
    model: qwen-image-2.0-pro
  omni:
    model: qwen3.5-omni-plus
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

完整 **REST 路径表、鉴权、声音克隆 REST、WebSocket 实时 TTS 协议** 见下文 **[HTTP API 路由与实时语音](#http-api-路由与实时语音)**；此处为最小上手示例。

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

### HTTP API 路由与实时语音

下面是与 `go run ./cmd/fullmodel serve` 对应的**路由表**、**鉴权规则**，以及 **WebSocket 实时 TTS** 的协议说明。JSON 请求体、`kind` 取值、响应 envelope、任务与 Artifacts 等仍见本文后面的「HTTP 请求格式」「长任务」等小节。

#### 启动参数

```bash
go run ./cmd/fullmodel serve \
  -addr 127.0.0.1:8080 \
  [-config path/to/llm.yaml] \
  [-task-workers 4] \
  [-api-key <key>]
```

- **`-api-key`**：为空时表示**不启用** HTTP 鉴权。若命令行未写 `-api-key` 且环境中已设置 **`FULLMODEL_API_KEY`**，则 **serve 会默认使用该值作为 API Key**（便于与下方 `curl -H "Authorization: Bearer $FULLMODEL_API_KEY"` 一致）。本地调试若不想鉴权，可先 `unset FULLMODEL_API_KEY` 或显式传入 `-api-key ""`。
- 语音相关能力依赖 **`config/llm.yaml`** 中的 **`brains.voice`** 与 **`DASHSCOPE_API_KEY`**（或其它 profile 密钥）。

#### 鉴权（可选）

启用 API Key 后以下方式任选其一：

| 方式 | 用法 |
|------|------|
| Header | `X-API-Key: <key>` |
| Header | `Authorization: Bearer <key>` |
| Query | `?api_key=<key>`（适合浏览器 **WebSocket**，无法自定义 Header 时） |

#### 路由一览

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/v1/capabilities` | 已注册能力列表 |
| `POST` | `/v1/messages` | 统一消息入口（JSON 或 `multipart/form-data`） |
| `POST` | `/v1/chat` | 聊天快捷入口（JSON，`kind` 默认 `text`） |
| `POST` | `/v1/tasks` | 创建异步任务 |
| `GET` | `/v1/tasks` | 列出任务 |
| `GET` | `/v1/tasks/{id}` | 查询任务状态与结果 |
| `DELETE` | `/v1/tasks/{id}` | 取消任务 |
| `GET` | `/v1/artifacts` | 列出工件 |
| `GET` | `/v1/artifacts/{id}` | 下载二进制工件（音频/图片等） |
| `GET` | `/v1/sessions/{id}` | 查看会话消息列表 |
| `DELETE` | `/v1/sessions/{id}` | 清除会话 |
| `GET` | `/v1/tools` | 已注册工具元数据 |
| `GET` | `/v1/voice/customizations` | 列出已注册的克隆音色（Qwen voice enrollment） |
| `POST` | `/v1/voice/customizations` | 提交样本音频，创建克隆音色 |
| `DELETE` | `/v1/voice/customizations/{voice}` | 按音色 id 删除克隆配置 |
| `GET` | `/v1/voice/tts/stream` | **升级到 WebSocket**：实时语音合成流（见下） |
| `GET` | `/v1/voice/asr/stream` | **升级到 WebSocket**：双工语音识别（二进制推流；下行 `partial` / `final`；可与页面 SSE、`/v1/voice/tts/stream` 自行编排） |
| `GET` | `/v1/voice/dialog/stream` | **升级到 WebSocket**：单连接串联「麦克风→ASR→流式 Chat→实时 TTS」（便捷编排；见 **[语音：三块开放能力与编排](#语音三块开放能力与调用侧编排)**） |

#### 语音：三块开放能力与编排 {#语音三块开放能力与调用侧编排}

`fullmodel serve` 把端到端口语对话需要的 primitive 拆成 **三类可独立调用的能力**（HTTP / WebSocket），**不在服务端替你规定**「谁先谁后、是否要定稿再上屏」。**网关、App、Agent** 可自行组合：**流式语音识别只点亮 UI**，**送进 LLM 的用户话用本轮定稿**，**助手侧流式正文 + 流式 TTS**，等等。

| 能力 | Serve 暴露（当前） | 说明 |
|------|---------------------|------|
| **语音识别（→ 文本）** | **`GET /v1/voice/asr/stream`** → **升级到 WebSocket**（双工推流：`start`→二进制 PCM/WAV、`finish`，下行 `partial` / `final`）；以及 **`POST /v1/messages`**，`kind` **`speech_to_text`**，`multipart/form-data` **整段**音频 | **推流**：边录边转、UI 可先刷 `partial`，**业务定稿可用 `final` 再走 LLM**（由调用方决定）。**Multipart**：一轮完整文件转写（已录完再请求）。底层 Fun-ASR，模型见 **`brains.asr`**。 |
| **LLM（文本理解与生成）** | **`POST /v1/messages`** 或 **`POST /v1/chat`**，`"stream": true` | HTTP 响应为 **`text/event-stream`（SSE）**，按事件推送助手增量文本；可加 **`session_id`** 与会话记忆。 |
| **流式语音合成** | **`GET /v1/voice/tts/stream`** → **升级到 WebSocket** | 下发 **PCM**（二进制）与原厂商事件 JSON（`response.audio.delta`、`session.finished` 等）；音色 / `voice_realtime_ws` 模型等与文档「实时语音合成」一节一致。 |

**便捷串联（非必须）：** **`GET /v1/voice/dialog/stream`**（WebSocket）在 **单连接** 内做完「binary 麦克风 → **`op:asr` JSON → 二进制 TTS → `op:assistant`」**，等价于对上述三件的 **reference 编排**。集成方若为最低延迟或可观测性拆解管线，可直接调三个底层入口，无需使用 dialog。流式 ASR 的握手与载荷细节见 **[WebSocket：流式语音识别](#websocket-voice-asr-stream)**。

**Roadmap · 能力与文档（建议实施顺序）：**

1. ~~**流式语音识别 API**：~~已实现 **`GET /v1/voice/asr/stream`**（WebSocket 双工：**partial / final**）。**何时用 `final` 调 LLM** 仍由调用方决定；建议在 UI 上对 `partial` 与定稿分界清楚。后续可补强 OpenAPI、`capabilities` 中的条目与字段说明。  
2. **capabilities / OpenAPI**：在 **`GET /v1/capabilities`**（或等价 OpenAPI）中列出 **`speech_stream`、`chat_stream`、`tts_stream`**（名称可再定），标注鉴权、`stream` SSE 与 WS 升级路径，便于自动生成客户端。  
3. **语义与节流**：三块接口独立限流 / 熔断 / 会话隔离；对话框场景在文档中给出 **推荐序列**（仅「建议」，非约束）。  
4. **观测与 SLA**：为三块分别打点（首 partial、首 SSE chunk、首 PCM、错误码）；`[voice.dialog]` 可作为 **串联范例**保留。  
5. **示例**：仓库内分别提供最小 **只做 ASR-WS / 只做 Chat-SSE / 只做 TTS-WS** 的示例脚本，再在文档中 **组合一章**「低延迟口述」的典型调用图。

完成后，延迟优化可由调用方在 **streaming ASR + 定稿后 Chat + phrase-chunk TTS** 路径上独立完成，服务端只保证 **每一块 API 的稳定与文档一致**。

#### REST：声音克隆（Qwen Voice Enrollment）

对接 DashScope **音色定制 / 克隆** HTTP API（`qwen-voice-enrollment`）。需有效的 `DASHSCOPE_API_KEY` 及百炼侧权限；返回的 **`voice` id** 可用于后续 **Qwen 实时 TTS** WebSocket 的 `voice` 查询参数（需与 `target_model` 支持的音色体系一致，见阿里云文档）。

**`GET /v1/voice/customizations`**

- Query：`page_size`、`page_index`（可选，与 SDK `VoiceListRequest` 一致）
- 响应：`result` 内为 `VoiceListResult`（`voices`、`request_id`、`usage` 等），与其它接口相同 envelope。

**`POST /v1/voice/customizations`**

- **`fullmodel serve`**：**`target_model`** 在未传时使用 **`brains.voice_realtime_ws.model`**（与 **`GET /v1/voice/tts/stream`** 默认 realtime **`model`** 同源），网关/前端不必再推导；显式 **`target_model`** 仍可覆盖。不经 serve、只走 **`CloneVoice`** 时使用 SDK **`voiceClonePayload`** 自带的默认 **`target_model`**。
- **JSON**：`application/json`，字段示例：

```json
{
  "preferred_name": "my_voice",
  "target_model": "qwen3-tts-vc-realtime-2026-01-15",
  "language": "zh",
  "text": "可选，样本对齐文本",
  "audio_url": "https://example.com/sample.mp3",
  "audio_mime_type": "audio/mpeg",
  "audio_data": "BASE64_RAW",
  "audio_data_url": "data:audio/mpeg;base64,..."
}
```

（`audio_url`、`audio_data`、`audio_data_url` 三选一；与 SDK `VoiceCloneRequest` 一致。）

- **`multipart/form-data`**：表单字段 `preferred_name`、`target_model`（**serve 可省略**，见上文）、`language`、`text`、`model`；音频任选 `audio` 文件、`audio_url` 或 `audio_data_url`。

**`DELETE /v1/voice/customizations/{voice}`**

- 路径最后一段为服务端返回的 **`voice` id**（请对特殊字符做 URL 编码）。

**curl 示例**

```bash
# 列表
curl -s "http://127.0.0.1:8080/v1/voice/customizations?page_size=20&page_index=1"

# 克隆（上传文件；不写 target_model 时由 serve 使用 brains.voice_realtime_ws.model）
curl -s http://127.0.0.1:8080/v1/voice/customizations \
  -F preferred_name=demo_voice_auto_model \
  -F language=zh \
  -F audio=@./sample.mp3

# 克隆（显式 target_model，覆盖配置）
curl -s http://127.0.0.1:8080/v1/voice/customizations \
  -F preferred_name=demo_voice \
  -F target_model=qwen3-tts-vc-realtime-2026-01-15 \
  -F language=zh \
  -F audio=@./sample.mp3

# 删除
curl -s -X DELETE "http://127.0.0.1:8080/v1/voice/customizations/<voice_id>"
```

#### WebSocket：流式语音识别（`/v1/voice/asr/stream`） {#websocket-voice-asr-stream}

与 **整段 multipart** 的 `speech_to_text` 不同，本入口为 **双工 WebSocket**：客户端持续发送 **音频二进制帧**，服务端将 Fun-ASR **实时结果**以 JSON 回推。模型由 **`brains.asr.model`** 决定（默认与文档示例一致，如 `fun-asr-realtime`）。实现见 `cmd/fullmodel/voice_asr_stream.go`。

**典型用途**：页面上 **`partial` 先出字**；**`final` 作为本轮定稿**再调 **`POST /v1/messages`**（`"stream": true`，SSE）与 **`GET /v1/voice/tts/stream`** 做低延迟编排。仓库参考页：`examples/voice_dialog_web/index.html`（模式 C）。

**握手**

- 方法 **`GET`**，路径 **`/v1/voice/asr/stream`**，协议升级为 WebSocket（`ws://` / `wss://`）。
- 查询参数（可选）：**`api_key`** — 与 HTTP 一样在浏览器侧无法自定义 Header 时用（与 `GET /v1/voice/tts/stream` 相同约定）。

**客户端 → 服务端**

| 类型 | 内容 | 说明 |
|------|------|------|
| 文本 JSON | `{"op":"ping"}` | 心跳；服务端返回 `pong`。 |
| 文本 JSON | `{"op":"start","format":"pcm","sample_rate":16000}` | 开始一轮识别。`format`：`pcm` 或 **`wav`**，缺省 **`pcm`**；`sample_rate` 缺省 **16000**。发送 **`start`** 会结束上一轮上游任务并重开。 |
| **二进制** | 原始音频块 | **仅在收到 `started` 之后**发送；`pcm` 为 **s16le 单声道**，`sample_rate` 与 **`start`** 一致。单轮累计超过约 **20 MiB** 时服务端报错并断开上游。 |
| 文本 JSON | `{"op":"finish"}` | 告知本轮音频结束；服务端向上游发 `finish-task`，随后在下游发 **`final`**（见下）。需已 **`start`** 且上游仍为 active。 |

**服务端 → 客户端（文本 JSON）**

| `op` | 字段（节选） | 含义 |
|------|----------------|------|
| `welcome` | `path` | 连接就绪。 |
| `pong` | `t` | 毫秒时间戳。 |
| `started` | `format`, `sample_rate`, `task_id` | 上游 **`task-started`** 已就绪，可开始发二进制音频。 |
| `partial` | `text`, `sentence_end`, `task_id` | 中间识别；`sentence_end: true` 表示一句边界（便于分段上屏）。 |
| `final` | `text`, `task_id` | 本轮定稿（多句时用换行拼接）；收到后再走 LLM / TTS 由调用方决定。 |
| `error` | `message`, `task_id`（可选） | 错误说明。 |

浏览器调试时注意：静态页与 API **不同端口**时 ，需 **`fullmodel serve` 启用 CORS**（见 serve 初始化）或将前后端同源代理到同一主机。

#### WebSocket：实时语音合成（Qwen Realtime TTS）

终端或浏览器通过 **`ws://`**（或 **`wss://`**）连接上述路径。服务端再与阿里云 DashScope **Qwen 实时 TTS** WebSocket 建连；这与 **`POST /v1/messages`** 且 `kind` 为 **`text_to_speech`** 所使用的 **CosyVoice（inference）** 链路不同。

| 配置项 | 用途 |
|--------|------|
| `brains.voice.model`（如 **`cosyvoice-v3-flash`**） | **`/v1/messages`** 的 **`text_to_speech`** 等非实时 CosyVoice 链路 |
| `brains.asr.model`（如 **`fun-asr-realtime`**） | Fun-ASR： **`speech_to_text`**、**`/v1/voice/asr/stream`**、`/v1/voice/dialog/stream` 内的 ASR 等（勿把 TTS 模型 id 配进此项） |
| `brains.voice_realtime_ws.model` | **`fullmodel serve` 同源配置**：默认为 **`qwen3-tts-vc-realtime-2026-01-15`**（可被 YAML 覆盖）；既作为 **`GET /v1/voice/tts/stream`** 的首选 realtime **`model`（合并后非空时优先于 `?model=`），也用于 **`POST /v1/voice/customizations`** 在省略 **`target_model`** 时的填入，使克隆音色与默认实时合成闭环** |

**握手时的查询参数（可选）**

| 参数 | 说明 |
|------|------|
| `voice` | 音色：**内置名**（如 `Cherry`）或 **`POST /v1/voice/customizations` 返回的 `voice` id**（克隆）；须与 realtime **`model` / `target_model`** 体系一致 |
| `model` | **通常不必写**：服务端已用 **`brains.voice_realtime_ws.model`**（内置默认或与 YAML 一致）；仅在合并该项为空时才看 **`?model=`** 再走 SDK |
| `mode` | `server_commit`（默认）或 `commit` |
| `language_type` | 如 `Chinese` |
| `format` | 默认 `pcm` |
| `sample_rate` | 默认 `24000` |
| `instructions` | 合成风格说明 |
| `optimize_instructions` | `true` / `1` 表示优化指令 |
| `api_key` | 与 HTTP 鉴权相同（启用鉴权时） |

**客户端 → 服务端（文本帧 JSON）**

| `op` | 示例 | 含义 |
|------|------|------|
| `append` | `{"op":"append","text":"你好"}` | 追加待合成文本 |
| `commit` | `{"op":"commit"}` | 仅在 `mode=commit` 时需要 |
| `clear` | `{"op":"clear"}` | 清空文本缓冲 |
| `finish` | `{"op":"finish"}` | 结束本轮并关闭上游会话 |
| `ping` | `{"op":"ping"}` | 心跳；对等返回 `pong` |

**服务端 → 客户端**

- **二进制帧**：连续 **PCM**（与 `format`、`sample_rate` 一致；默认 16 位小端、单声道、24 kHz）。
- **文本帧 JSON**（节选）：
  - `{"op":"event","type":"<上游事件类型>"}`，例如 `session.created`、`response.done`、`session.finished`。
  - `{"op":"error","message":"...","error_code":"..."}`。
  - `{"op":"done"}`：本轮结束。
  - `{"op":"pong","t":<毫秒时间戳>}`。

**命令行联调示例**

```bash
# 终端 A
export DASHSCOPE_API_KEY="your-key"
go run ./cmd/fullmodel serve -addr 127.0.0.1:8080

# 终端 B（默认写出 tts_out.pcm）
go run ./examples/voice_tts_ws_client \
  -url "ws://127.0.0.1:8080/v1/voice/tts/stream" \
  -text "你好，测试实时 TTS"

# 若 serve 因 FULLMODEL_API_KEY 启用了鉴权，请传同一密钥，例如：
# go run ./examples/voice_tts_ws_client -apikey "$FULLMODEL_API_KEY" ...
```

**播放 PCM（FFmpeg / ffplay 8.x）**  
`ffplay` 对 raw PCM 不要使用 `-ac 1`（可能报 `Option not found`），可改用：

```bash
ffplay -f s16le -ar 24000 -ch_layout mono tts_out.pcm
```

**日志前缀（TTS WebSocket 桥）**：`[voice.tts.client]` 为客户端连 **fullmodel** 的 `GET /v1/voice/tts/stream`；`[voice.realtime_ws] leg=upstream` 为 fullmodel **出站** 与实时 TTS 的 WebSocket 握手（`endpoint_key=tts_realtime_ws` 等）；`[voice.tts.upstream]` 为握手成功后的上游协议（`session.update`、音频/事件）。实现见 `cmd/fullmodel/voice_stream.go`、`voice_realtime.go`。

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
  voice_realtime_ws:
    model: qwen3-tts-vc-realtime-2026-01-15
  image:
    model: qwen-image-2.0-pro
  omni:
    model: qwen3.5-omni-plus
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
  voice_realtime_ws:
    model: qwen3-tts-vc-realtime-2026-01-15
  image:
    model: qwen-image-2.0-pro
  omni:
    model: qwen3.5-omni-plus
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
