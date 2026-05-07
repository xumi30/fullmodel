package searchFunctions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/xumi30/fullmodel/utils"
)

type BaiduSearchTool struct{}

func NewBaiduSearchTool() *BaiduSearchTool {
	return &BaiduSearchTool{}
}

func (t *BaiduSearchTool) Name() string {
	return "baidu_search"
}

func (t *BaiduSearchTool) Description() string {
	return "Search the web using Baidu AI Search Engine. Use this tool for live information, Chinese web results, documentation, news, research topics, or anything that requires up-to-date web search results."
}

func (t *BaiduSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results to return, from 1 to 50. Default 10.",
			},
			"freshness": map[string]interface{}{
				"type":        "string",
				"description": "Optional time range: pd, pw, pm, py, or YYYY-MM-DDtoYYYY-MM-DD.",
			},
		},
		"required": []string{"query"},
	}
}

func (t *BaiduSearchTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Query     string `json:"query"`
		Count     int    `json:"count"`
		Freshness string `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(utils.ExtractJSON(args)), &params); err != nil {
		return "", fmt.Errorf("invalid baidu_search arguments: %w", err)
	}
	params.Query = strings.TrimSpace(params.Query)
	if params.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	count := params.Count
	if count <= 0 {
		count = 10
	}
	if count > 50 {
		count = 50
	}

	searchFilter, err := baiduFreshnessFilter(params.Freshness, time.Now())
	if err != nil {
		return "", err
	}
	requestBody := map[string]any{
		"messages": []map[string]string{
			{
				"content": params.Query,
				"role":    "user",
			},
		},
		"search_source":        "baidu_search_v2",
		"resource_type_filter": []map[string]any{{"type": "web", "top_k": count}},
		"search_filter":        searchFilter,
	}

	endpoint, headers, err := baiduSearchEndpoint()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("baidu search failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	if _, ok := decoded["code"]; ok {
		return "", fmt.Errorf("baidu search failed: %v", decoded["message"])
	}
	references, _ := decoded["references"].([]any)
	for _, item := range references {
		if obj, ok := item.(map[string]any); ok {
			delete(obj, "snippet")
		}
	}
	out, err := json.MarshalIndent(references, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *BaiduSearchTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": "Baidu web search references",
	}
}

func (t *BaiduSearchTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicSearch, "使用百度 AI 搜索获取实时网页结果，支持 freshness 与 count。")
}

func baiduSearchEndpoint() (string, map[string]string, error) {
	original := "https://qianfan.baidubce.com/v2/ai_search/web_search"
	headers := map[string]string{"Content-Type": "application/json"}

	sessionID := os.Getenv("DUMATE_SESSION_ID")
	schedulerURL := os.Getenv("DUMATE_SCHEDULER_URL")
	if sessionID == "" || schedulerURL == "" {
		apiKey := os.Getenv("BAIDU_API_KEY")
		if apiKey == "" {
			return "", nil, fmt.Errorf("BAIDU_API_KEY is not set")
		}
		headers["Authorization"] = "Bearer " + apiKey
		headers["X-Appbuilder-From"] = "openclaw"
		return original, headers, nil
	}

	parsed, err := url.Parse(original)
	if err != nil {
		return "", nil, err
	}
	proxyURL := strings.TrimRight(schedulerURL, "/") + "/api/qianfanproxy" + parsed.Path
	if parsed.RawQuery != "" {
		proxyURL += "?" + parsed.RawQuery
	}
	headers["Host"] = parsed.Host
	headers["X-Dumate-Session-Id"] = sessionID
	headers["X-Appbuilder-From"] = "desktop"
	return proxyURL, headers, nil
}

func baiduFreshnessFilter(freshness string, now time.Time) (map[string]any, error) {
	freshness = strings.TrimSpace(freshness)
	if freshness == "" {
		return map[string]any{}, nil
	}
	end := now.AddDate(0, 0, 1).Format("2006-01-02")
	var start string
	switch freshness {
	case "pd":
		start = now.AddDate(0, 0, -1).Format("2006-01-02")
	case "pw":
		start = now.AddDate(0, 0, -6).Format("2006-01-02")
	case "pm":
		start = now.AddDate(0, 0, -30).Format("2006-01-02")
	case "py":
		start = now.AddDate(0, 0, -364).Format("2006-01-02")
	default:
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}to\d{4}-\d{2}-\d{2}$`).MatchString(freshness) {
			return nil, fmt.Errorf("freshness must be pd, pw, pm, py, or YYYY-MM-DDtoYYYY-MM-DD")
		}
		parts := strings.Split(freshness, "to")
		start = parts[0]
		end = parts[1]
	}
	return map[string]any{
		"range": map[string]any{
			"page_time": map[string]any{
				"gte": start,
				"lt":  end,
			},
		},
	}, nil
}
