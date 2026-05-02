package brain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/utils/logging"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// setChatHeaders 设置 chat/completions 请求头（OpenAI 兼容）
func setChatHeaders(req *http.Request, cfg *Config) {
	req.Header.Set("Content-Type", "application/json")

	switch cfg.Provider {
	case ProviderAzure:
		req.Header.Set("api-key", cfg.APIKey)
	default:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	}

	req.Header.Set("User-Agent", "PeopleAgent/1.0")
}

func buildAzureChatCompletionsURL(cfg *Config) string {
	// 如果用户提供了自定义端点，直接使用
	if endpoint, ok := cfg.APIEndpoints["chat"]; ok {
		return endpoint
	}

	// 如果用户提供了基础URL，手动构建Azure格式的URL
	if cfg.BaseURL != "" && cfg.Model != "" {
		baseURL := strings.TrimRight(cfg.BaseURL, "/")
		return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2023-12-01-preview", baseURL, cfg.Model)
	}

	return cfg.BaseURL
}

// getChatCompletionsURL 获取 chat/completions 的完整 URL
func getChatCompletionsURL(cfg *Config) string {
	if cfg.BaseURL != "" {
		return normalizeChatCompletionsURL(cfg.BaseURL)
	}

	// 优先使用自定义端点映射
	if endpoint, ok := cfg.APIEndpoints["chat"]; ok {
		return endpoint
	}

	switch cfg.Provider {
	case ProviderQwen:
		switch cfg.Region {
		case "cn-beijing":
			return "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
		case "ap-southeast-1":
			return "https://dashscope-intl.aliyuncs.com/compatible-mode/v1/chat/completions"
		case "us-east-1":
			return "https://dashscope-us.aliyuncs.com/compatible-mode/v1/chat/completions"
		default:
			return "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
		}
	case ProviderOpenAI:
		return "https://api.openai.com/v1/chat/completions"
	case ProviderAzure:
		return buildAzureChatCompletionsURL(cfg)
	case ProviderGroq:
		return "https://api.groq.com/openai/v1/chat/completions"
	case ProviderTogether:
		return "https://api.together.xyz/v1/chat/completions"
	case ProviderLocalAI:
		return "http://localhost:8080/v1/chat/completions"
	case ProviderCustom:
		return ""
	default:
		return "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
	}
}

func normalizeChatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	trimmed := strings.TrimRight(baseURL, "/")
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "/chat/completions") ||
		strings.Contains(lower, "/deployments/") ||
		strings.Contains(lower, "?api-version=") {
		return baseURL
	}
	if strings.HasSuffix(lower, "/v1") || strings.HasSuffix(lower, "/compatible-mode/v1") {
		return trimmed + "/chat/completions"
	}
	return trimmed + "/chat/completions"
}

func createChatCompletion(ctx context.Context, httpClient *http.Client, cfg *Config, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = cfg.Model
	}

	requestBody, err := buildRequestBody(req)
	if err != nil {
		return nil, err
	}

	url := getChatCompletionsURL(cfg)
	logging.Info("[llm.request] nonstream start provider=%s model=%s url=%s messages=%d bodyBytes=%d tools=%d",
		cfg.Provider, req.Model, url, len(req.Messages), len(requestBody), len(req.Tools))
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	setChatHeaders(httpReq, cfg)

	start := time.Now()
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		logging.Error("[llm.request] nonstream transport failed provider=%s model=%s url=%s err=%v elapsed=%s",
			cfg.Provider, req.Model, url, err, time.Since(start))
		return nil, err
	}
	defer resp.Body.Close()
	logging.Info("[llm.response] nonstream status=%d provider=%s model=%s url=%s elapsed=%s",
		resp.StatusCode, cfg.Provider, req.Model, url, time.Since(start))

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logging.Error("[llm.response] nonstream error status=%d provider=%s model=%s body=%s",
			resp.StatusCode, cfg.Provider, req.Model, truncateForLog(string(body), 1200))
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var response ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logging.Error("[llm.response] nonstream decode failed provider=%s model=%s err=%v", cfg.Provider, req.Model, err)
		return nil, err
	}
	contentRunes := 0
	if len(response.Choices) > 0 {
		if content, ok := response.Choices[0].Message.Content.(string); ok {
			contentRunes = len([]rune(content))
		}
	}
	logging.Info("[llm.response] nonstream decoded provider=%s model=%s choices=%d contentRunes=%d totalTokens=%d",
		cfg.Provider, req.Model, len(response.Choices), contentRunes, response.Usage.TotalTokens)
	return &response, nil
}

func truncateForLog(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "...(truncated)"
}

// streamToolCallAcc 按 (choiceIndex, toolIndex) 累积单路流式 tool_calls（OpenAI / 千问兼容约定）
type streamToolCallAcc struct {
	id       string
	typ      string
	name     string
	args     strings.Builder
	toolIdx  int
	choiceIx int
}

func mergeStreamToolCallDeltas(
	byKey map[string]*streamToolCallAcc,
	choiceIndex int,
	deltas []ToolCall,
) (updated bool) {
	for _, d := range deltas {
		toolIdx := 0
		if d.Index != nil {
			toolIdx = *d.Index
		}
		key := streamToolCallKey(choiceIndex, toolIdx)
		a, ok := byKey[key]
		if !ok {
			a = &streamToolCallAcc{toolIdx: toolIdx, choiceIx: choiceIndex}
			byKey[key] = a
			updated = true
		}
		if d.ID != "" && d.ID != a.id {
			a.id = d.ID
			updated = true
		}
		if d.Type != "" && d.Type != a.typ {
			a.typ = d.Type
			updated = true
		}
		if d.Function.Name != "" && d.Function.Name != a.name {
			a.name = d.Function.Name
			updated = true
		}
		if d.Function.Arguments != "" {
			a.args.WriteString(d.Function.Arguments)
			updated = true
		}
	}
	return updated
}

func streamToolCallKey(choiceIndex, toolIndex int) string {
	return fmt.Sprintf("%d:%d", choiceIndex, toolIndex)
}

// snapshotStreamToolCalls 将累积状态转为有序 []ToolCall（仅指定 choiceIndex）
func snapshotStreamToolCalls(byKey map[string]*streamToolCallAcc, choiceIndex int) []ToolCall {
	type pair struct {
		toolIdx int
		acc     *streamToolCallAcc
	}
	var list []pair
	prefix := fmt.Sprintf("%d:", choiceIndex)
	for k, a := range byKey {
		if strings.HasPrefix(k, prefix) {
			list = append(list, pair{toolIdx: a.toolIdx, acc: a})
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].toolIdx < list[j].toolIdx })
	out := make([]ToolCall, 0, len(list))
	for _, p := range list {
		idx := p.toolIdx
		out = append(out, ToolCall{
			Index:    &idx,
			ID:       p.acc.id,
			Type:     p.acc.typ,
			Function: FunctionCall{Name: p.acc.name, Arguments: p.acc.args.String()},
		})
	}
	return out
}

func createChatCompletionStream(ctx context.Context, httpClient *http.Client, cfg *Config, req ChatCompletionRequest) (*BrainOutput, error) {
	streamOut := newStreamOutput(ctx, 64)
	usage := &Usage{}

	go func() {
		defer streamOut.complete(nil)

		toolAccByKey := make(map[string]*streamToolCallAcc)

		req.Stream = true
		req.StreamOptions = &StreamOptions{IncludeUsage: true}

		requestBody, err := buildRequestBody(req)
		if err != nil {
			streamOut.fail(err)
			return
		}

		logging.Info("Request body: %s", string(requestBody))
		httpReq, err := http.NewRequestWithContext(ctx, "POST", getChatCompletionsURL(cfg), bytes.NewReader(requestBody))
		if err != nil {
			streamOut.fail(err)
			return
		}
		setChatHeaders(httpReq, cfg)

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			streamOut.fail(err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			streamOut.fail(fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body)))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		// 默认 token 限制较小（64K），长内容可能被截断导致扫描提前失败
		scanner.Buffer(make([]byte, 16*1024), 2*1024*1024)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				streamOut.fail(ctx.Err())
				return
			default:
			}

			line := scanner.Text()
			if line == "" {
				continue
			}

			if after, ok := strings.CutPrefix(line, "data: "); ok {
				data := after
				if data == "[DONE]" {
					if snap := snapshotStreamToolCalls(toolAccByKey, 0); len(snap) > 0 {
						select {
						case <-ctx.Done():
							streamOut.fail(ctx.Err())
							return
						case streamOut.toolCh <- snap:
						}
					}
					return
				}

				var chunk ChatCompletionChunk
				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					streamOut.fail(fmt.Errorf("failed to decode SSE data chunk: %w", err))
					return
				}

				if chunk.Usage != nil {
					usage.PromptTokens = chunk.Usage.PromptTokens
					usage.CompletionTokens = chunk.Usage.CompletionTokens
					usage.TotalTokens = chunk.Usage.TotalTokens
				}

				for _, choice := range chunk.Choices {
					ci := choice.Index
					if len(choice.Delta.ToolCalls) > 0 {
						if mergeStreamToolCallDeltas(toolAccByKey, ci, choice.Delta.ToolCalls) {
							snap := snapshotStreamToolCalls(toolAccByKey, ci)
							if len(snap) > 0 {
								// 对外只推送首条 choice 的累积结果（与常见 n=1 流式一致）
								if ci == 0 {
									select {
									case <-ctx.Done():
										streamOut.fail(ctx.Err())
										return
									case streamOut.toolCh <- snap:
									}
								}
							}
						}
					}
					if choice.Delta.Content != "" {
						select {
						case <-ctx.Done():
							streamOut.fail(ctx.Err())
							return
						case streamOut.textCh <- choice.Delta.Content:
						}
					}
					if choice.FinishReason == "tool_calls" {
						if snap := snapshotStreamToolCalls(toolAccByKey, ci); len(snap) > 0 && ci == 0 {
							select {
							case <-ctx.Done():
								streamOut.fail(ctx.Err())
								return
							case streamOut.toolCh <- snap:
							}
						}
					}
				}
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			streamOut.fail(fmt.Errorf("stream scanner error: %w", err))
			return
		}

		// 部分实现不发送 [DONE]；结束前再推一次首条 choice 的工具快照
		if snap := snapshotStreamToolCalls(toolAccByKey, 0); len(snap) > 0 {
			select {
			case <-ctx.Done():
				streamOut.fail(ctx.Err())
				return
			case streamOut.toolCh <- snap:
			}
		}
	}()

	return &BrainOutput{
		Status: BrainStatus{Success: true},
		Stream: streamOut,
		Usage:  usage,
	}, nil
}
