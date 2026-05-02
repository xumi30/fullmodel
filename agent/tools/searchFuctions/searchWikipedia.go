package searchFunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/utils"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WikipediaSearchTool struct{}

func NewWikipediaSearchTool() *WikipediaSearchTool {
	return &WikipediaSearchTool{}
}

func (t *WikipediaSearchTool) Name() string {
	return "wiki_search"
}

func (t *WikipediaSearchTool) Description() string {
	return `
Search Wikipedia (zh) using the official MediaWiki API.

Use this tool when the user explicitly wants Wikipedia-sourced results or encyclopedic definitions.

Endpoint:
- https://zh.wikipedia.org/w/api.php?action=query&list=search&format=json&srsearch=...
`
}

func (t *WikipediaSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query (srsearch)",
			},
			"num_results": map[string]interface{}{
				"type":        "integer",
				"description": "Max results (srlimit), default 5, max 20",
			},
		},
		"required": []string{"query"},
	}
}

type wikipediaSearchAPIResp struct {
	Query struct {
		Search []struct {
			PageID  int    `json:"pageid"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"search"`
	} `json:"query"`
}

type WikipediaSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	PageID  int    `json:"pageid"`
}

func (t *WikipediaSearchTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *WikipediaSearchTool) Execute(ctx context.Context, args string) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	qv, _ := params["query"].(string)
	query := strings.TrimSpace(qv)
	if query == "" {
		return "", fmt.Errorf("query 不能为空")
	}

	limit := 5
	if num, ok := params["num_results"].(float64); ok {
		limit = int(num)
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	apiURL := "https://zh.wikipedia.org/w/api.php"
	u, _ := url.Parse(apiURL)
	qs := u.Query()
	qs.Set("action", "query")
	qs.Set("list", "search")
	qs.Set("format", "json")
	qs.Set("srsearch", query)
	qs.Set("srlimit", fmt.Sprintf("%d", limit))
	qs.Set("utf8", "1")
	u.RawQuery = qs.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "peopleAgentAPP/1.0 (WikipediaSearchTool)")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("wikipedia api HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var apiResp wikipediaSearchAPIResp
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&apiResp); err != nil {
		return "", err
	}

	results := make([]WikipediaSearchResult, 0, len(apiResp.Query.Search))
	for _, r := range apiResp.Query.Search {
		title := strings.TrimSpace(r.Title)
		if title == "" || r.PageID == 0 {
			continue
		}
		results = append(results, WikipediaSearchResult{
			Title:   title,
			PageID:  r.PageID,
			URL:     fmt.Sprintf("https://zh.wikipedia.org/?curid=%d", r.PageID),
			Snippet: stripHTML(r.Snippet),
		})
	}

	out, _ := json.MarshalIndent(map[string]interface{}{
		"results": results,
	}, "", "  ")
	return string(out), nil
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = htmlTagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#039;", `'`)
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return strings.TrimSpace(s)
}

func (t *WikipediaSearchTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicSearch, "调用中文维基 MediaWiki API 检索百科条目标题与摘要。")
}

func (t *WikipediaSearchTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Wikipedia search results (wrapped object).",
		"properties": map[string]interface{}{
			"results": map[string]interface{}{
				"type":        "array",
				"description": "Array of Wikipedia search results",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title": map[string]interface{}{
							"type":        "string",
							"description": "Wikipedia page title",
						},
						"url": map[string]interface{}{
							"type":        "string",
							"description": "Wikipedia page url (curid link)",
						},
						"snippet": map[string]interface{}{
							"type":        "string",
							"description": "Short snippet from Wikipedia search (plain text)",
						},
						"pageid": map[string]interface{}{
							"type":        "integer",
							"description": "Wikipedia page id",
						},
					},
				},
			},
		},
	}
}
