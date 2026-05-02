# AI 模型配置

配置文件：`config/llm.yaml`

最常用的写法只需要一份公共配置，加上各能力的模型名：

```yaml
defaults:
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

`defaults` 会先应用到所有能力，`brains.<name>` 只覆盖自己的差异字段。

支持的能力名：

| 名称 | 用途 | 默认模型 |
|------|------|----------|
| `text` | 文本对话 | `qwen-plus` |
| `vision` | 图片/视频理解 | `qwen-vl-plus` |
| `voice` | 语音合成/识别 | `cosyvoice-v3-flash` |
| `image` | 图像生成/编辑 | `qwen-image-2.0-pro` |

可配置字段：

```yaml
api_key: ${DASHSCOPE_API_KEY}
base_url: https://example.com/v1
model: qwen-plus
provider: qwen
region: cn-beijing
endpoints:
  chat: https://example.com/v1/chat/completions
```

示例：文本走 OpenAI，其他能力走 DashScope：

```yaml
defaults:
  api_key: ${DASHSCOPE_API_KEY}
  provider: qwen
  region: cn-beijing

brains:
  text:
    api_key: ${OPENAI_API_KEY}
    provider: openai
    model: gpt-4o
  vision:
    model: qwen-vl-plus
  voice:
    model: cosyvoice-v3-flash
  image:
    model: qwen-image-2.0-pro
```

环境变量会自动展开，例如 `${DASHSCOPE_API_KEY}`。
