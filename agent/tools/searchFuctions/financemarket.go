package searchFunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"fullmodel/utils"
	"io"

	"net/http"
	"strings"
	"time"
)

// MarketTool implements market data query
type MarketTool struct{}

func NewMarketTool() *MarketTool {
	return &MarketTool{}
}

func (t *MarketTool) Name() string {
	return "query_market"
}

func (t *MarketTool) Description() string {
	return `
Query financial market data and generate structured analysis.

Use this tool when:
- The user asks about stock market, crypto, or financial markets
- The query involves price, trend, or investment conditions
- The user asks about "today market", "market situation", "price movement"

You MUST use this tool instead of general search when the question is about financial markets.

This tool provides structured insights including:
- Trend analysis
- Volatility
- Risk level
- Market sentiment

Valid symbol examples:
- Stocks: AAPL, MSFT, GOOGL, TSLA
- Crypto: BTC-USD, ETH-USD
- Indices: ^GSPC (S&P 500), ^DJI (Dow Jones)

IMPORTANT: Always provide a valid market symbol in the 'symbol' parameter.
If user doesn't specify a symbol, use a default like 'AAPL' or ask the user.

Do NOT rely on search tools for market data if this tool is available.
`
}

func (t *MarketTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Market symbol (e.g., AAPL, BTC-USD, ^GSPC)",
			},
		},
		"required": []string{"symbol"},
	}
}

func (t *MarketTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *MarketTool) Execute(ctx context.Context, args string) (string, error) {
	// 解析输入参数
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid input format: %v", err)
	}

	// 获取并规范化符号
	rawSymbol, _ := params["symbol"].(string)
	symbol := normalizeSymbol(rawSymbol)

	// 获取市场数据
	q, err := Get(symbol)
	if err != nil {
		return "", fmt.Errorf("failed to fetch market data: %v", err)
	}

	// =========================
	// 安全提取数据
	// =========================
	price := q.RegularMarketPrice
	open := q.RegularMarketOpen
	prevClose := q.RegularMarketPreviousClose
	volume := q.RegularMarketVolume

	change := price - prevClose

	changePct := 0.0
	if prevClose != 0 {
		changePct = (change / prevClose) * 100
	}

	// =========================
	// 趋势判断
	// =========================
	trend := "sideways"
	if changePct > 1 {
		trend = "uptrend"
	} else if changePct < -1 {
		trend = "downtrend"
	}

	// =========================
	// 波动率（简化）
	// =========================
	volatility := "low"
	if open != 0 {
		if (price-open)/open > 0.02 {
			volatility = "high"
		} else if (price-open)/open > 0.01 {
			volatility = "medium"
		}
	}

	// =========================
	// Volume 安全判断
	// =========================
	volumeTrend := "unknown"
	if volume > 0 && prevClose > 0 {
		volumeTrend = "stable"
	}

	// =========================
	// 风险评分（简化模型）
	// =========================
	risk := "low"
	if volatility == "high" {
		risk = "high"
	} else if volatility == "medium" {
		risk = "medium"
	}

	// =========================
	// 输出结构（稳定 JSON）
	// =========================
	result := map[string]interface{}{
		"symbol":    symbol,
		"timestamp": time.Now().Format(time.RFC3339),

		"summary": map[string]interface{}{
			"current_price":  price,
			"open":           open,
			"previous_close": prevClose,
			"change":         change,
			"change_percent": fmt.Sprintf("%.2f%%", changePct),
			"volume":         volume,
			"volume_trend":   volumeTrend,
			"trend":          trend,
			"volatility":     volatility,
		},

		"analysis": map[string]interface{}{
			"market_sentiment": "neutral",
			"risk_level":       risk,
			"liquidity":        "normal",
		},

		"insights": []string{
			fmt.Sprintf("Market is in %s phase", trend),
			fmt.Sprintf("Volatility is %s", volatility),
		},
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// Quote represents financial market data
type Quote struct {
	Symbol                     string  `json:"symbol"`
	RegularMarketPrice         float64 `json:"regularMarketPrice"`
	RegularMarketOpen          float64 `json:"regularMarketOpen"`
	RegularMarketPreviousClose float64 `json:"regularMarketPreviousClose"`
	RegularMarketVolume        int64   `json:"regularMarketVolume"`
}

// Get fetches market data for the given symbol
func Get(symbol string) (*Quote, error) {
	// 创建 HTTP 请求
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s", symbol)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	// 解析响应
	var result struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol                     string  `json:"symbol"`
					RegularMarketPrice         float64 `json:"regularMarketPrice"`
					RegularMarketOpen          float64 `json:"regularMarketOpen"`
					RegularMarketPreviousClose float64 `json:"regularMarketPreviousClose"`
					RegularMarketVolume        int64   `json:"regularMarketVolume"`
				} `json:"meta"`
			} `json:"result"`
		} `json:"chart"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Chart.Result) == 0 {
		return nil, fmt.Errorf("no data found for symbol: %s", symbol)
	}

	meta := result.Chart.Result[0].Meta

	return &Quote{
		Symbol:                     meta.Symbol,
		RegularMarketPrice:         meta.RegularMarketPrice,
		RegularMarketOpen:          meta.RegularMarketOpen,
		RegularMarketPreviousClose: meta.RegularMarketPreviousClose,
		RegularMarketVolume:        meta.RegularMarketVolume,
	}, nil
}

// YahooResponse represents the response from Yahoo Finance API
type YahooResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency             string  `json:"currency"`
				Symbol               string  `json:"symbol"`
				ExchangeName         string  `json:"exchangeName"`
				InstrumentType       string  `json:"instrumentType"`
				FirstTradeDate       int64   `json:"firstTradeDate"`
				Gmtoffset            int     `json:"gmtoffset"`
				Timezone             string  `json:"timezone"`
				ExchangeTimezoneName string  `json:"exchangeTimezoneName"`
				RegularMarketPrice   float64 `json:"regularMarketPrice"`
				ChartPreviousClose   float64 `json:"chartPreviousClose"`
				PriceHint            int     `json:"priceHint"`
				CurrentTradingPeriod struct {
					Pre struct {
						Timezone  string `json:"timezone"`
						Start     int64  `json:"start"`
						End       int64  `json:"end"`
						Gmtoffset int    `json:"gmtoffset"`
					} `json:"pre"`
					Regular struct {
						Timezone  string `json:"timezone"`
						Start     int64  `json:"start"`
						End       int64  `json:"end"`
						Gmtoffset int    `json:"gmtoffset"`
					} `json:"regular"`
					Post struct {
						Timezone  string `json:"timezone"`
						Start     int64  `json:"start"`
						End       int64  `json:"end"`
						Gmtoffset int    `json:"gmtoffset"`
					} `json:"post"`
				} `json:"currentTradingPeriod"`
				DataGranularity string   `json:"dataGranularity"`
				Range           string   `json:"range"`
				ValidRanges     []string `json:"validRanges"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []float64 `json:"open"`
					High   []float64 `json:"high"`
					Low    []float64 `json:"low"`
					Close  []float64 `json:"close"`
					Volume []int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// formatMarketData converts Yahoo Finance API response to structured market analysis
func (t *MarketTool) formatMarketData(data []byte, symbol string) (string, error) {
	var res YahooResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return "", fmt.Errorf("failed to parse Yahoo Finance response: %v", err)
	}

	// Check for API errors
	if res.Chart.Error.Code != "" {
		return "", fmt.Errorf("Yahoo Finance API error: %s - %s", res.Chart.Error.Code, res.Chart.Error.Description)
	}

	// Check if we have any results
	if len(res.Chart.Result) == 0 {
		return "", fmt.Errorf("no market data found for symbol '%s'. Please check if symbol is valid and try again", symbol)
	}

	// Get the first result
	result := res.Chart.Result[0]

	// Check if we have quote data
	if len(result.Indicators.Quote) == 0 {
		return "", fmt.Errorf("no quote data available for symbol '%s'", symbol)
	}

	prices := result.Indicators.Quote[0].Close
	volumes := result.Indicators.Quote[0].Volume

	n := len(prices)
	if n < 2 {
		return "", fmt.Errorf("insufficient data points for analysis for symbol '%s'", symbol)
	}

	start := prices[0]
	end := prices[n-1]

	// Calculate price change
	change := end - start
	changePct := (change / start) * 100

	// Calculate volatility
	maxPrice, minPrice := start, start
	for _, p := range prices {
		if p > maxPrice {
			maxPrice = p
		}
		if p < minPrice {
			minPrice = p
		}
	}

	volatility := (maxPrice - minPrice) / start * 100

	// Determine trend
	trend := "sideways"
	if changePct > 2 {
		trend = "uptrend"
	} else if changePct < -2 {
		trend = "downtrend"
	}

	// Determine volume trend
	volumeTrend := "stable"
	if n > 0 && volumes[n-1] > volumes[0]*2 {
		volumeTrend = "increasing"
	}

	// Determine risk level
	risk := "low"
	if volatility > 5 {
		risk = "medium"
	}
	if volatility > 10 {
		risk = "high"
	}

	// Determine market sentiment
	sentiment := "neutral"
	if trend == "uptrend" && volumeTrend == "increasing" {
		sentiment = "bullish"
	}
	if trend == "downtrend" {
		sentiment = "bearish"
	}

	// Create result structure
	resultData := map[string]interface{}{
		"symbol": symbol,
		"summary": map[string]interface{}{
			"current_price":  end,
			"change_percent": fmt.Sprintf("%.2f%%", changePct),
			"trend":          trend,
			"volatility":     fmt.Sprintf("%.2f%%", volatility),
			"volume_trend":   volumeTrend,
		},
		"analysis": map[string]interface{}{
			"market_sentiment": sentiment,
			"risk_level":       risk,
		},
		"insights": generateInsights(trend, risk, sentiment),
	}

	jsonBytes, err := json.MarshalIndent(resultData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result data: %v", err)
	}

	return string(jsonBytes), nil
}

// generateInsights generates market insights based on trend, risk, and sentiment
func generateInsights(trend, risk, sentiment string) []string {
	var insights []string

	if trend == "uptrend" {
		insights = append(insights, "Market is trending upward")
	}
	if trend == "downtrend" {
		insights = append(insights, "Market is trending downward")
	}
	if risk == "high" {
		insights = append(insights, "High volatility - risk is elevated")
	}
	if sentiment == "bullish" {
		insights = append(insights, "Bullish sentiment detected")
	}
	if sentiment == "bearish" {
		insights = append(insights, "Bearish sentiment detected")
	}

	return insights
}

func normalizeSymbol(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))

	if input == "" {
		return "AAPL"
	}

	switch input {

	// ===== 黄金 =====
	case "gold", "黄金", "金价", "黄金价格", "xau-usd", "xauusd":
		return "GC=F"

	// ===== 比特币 =====
	case "btc", "bitcoin", "比特币":
		return "BTC-USD"

	// ===== 以太坊 =====
	case "eth", "ethereum", "以太坊":
		return "ETH-USD"

	// ===== 美股 =====
	case "apple", "苹果":
		return "AAPL"
	case "tesla", "特斯拉":
		return "TSLA"
	case "google", "谷歌":
		return "GOOGL"
	case "amazon", "亚马逊":
		return "AMZN"

	// ===== 指数 =====
	case "sp500", "s&p500", "标普500":
		return "^GSPC"

	default:
		return input
	}
}

func (t *MarketTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicSearch, "按交易标的查询股票、加密等金融市场价格与趋势类结构化分析。")
}

func (t *MarketTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Structured market data analysis including price, trend, volatility, and insights",
		"properties": map[string]interface{}{
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Market symbol (e.g., AAPL, BTC-USD)",
				"example":     "AAPL",
			},
			"timestamp": map[string]interface{}{
				"type":        "string",
				"description": "Timestamp of the data in RFC3339 format",
				"example":     "2024-01-15T10:30:00Z",
			},
			"summary": map[string]interface{}{
				"type":        "object",
				"description": "Summary of market data",
				"properties": map[string]interface{}{
					"current_price": map[string]interface{}{
						"type":        "number",
						"description": "Current market price",
						"example":     150.25,
					},
					"open": map[string]interface{}{
						"type":        "number",
						"description": "Opening price for the current period",
						"example":     148.50,
					},
					"previous_close": map[string]interface{}{
						"type":        "number",
						"description": "Previous closing price",
						"example":     149.75,
					},
					"change": map[string]interface{}{
						"type":        "number",
						"description": "Price change from previous close",
						"example":     0.50,
					},
					"change_percent": map[string]interface{}{
						"type":        "string",
						"description": "Percentage change from previous close",
						"example":     "0.33%",
					},
					"volume": map[string]interface{}{
						"type":        "integer",
						"description": "Trading volume",
						"example":     1000000,
					},
					"volume_trend": map[string]interface{}{
						"type":        "string",
						"description": "Volume trend (stable, increasing, decreasing)",
						"example":     "stable",
					},
					"trend": map[string]interface{}{
						"type":        "string",
						"description": "Market trend (uptrend, downtrend, sideways)",
						"example":     "uptrend",
					},
					"volatility": map[string]interface{}{
						"type":        "string",
						"description": "Volatility level (low, medium, high)",
						"example":     "low",
					},
				},
			},
			"analysis": map[string]interface{}{
				"type":        "object",
				"description": "Market analysis and indicators",
				"properties": map[string]interface{}{
					"market_sentiment": map[string]interface{}{
						"type":        "string",
						"description": "Overall market sentiment (bullish, bearish, neutral)",
						"example":     "bullish",
					},
					"risk_level": map[string]interface{}{
						"type":        "string",
						"description": "Risk level (low, medium, high)",
						"example":     "low",
					},
					"liquidity": map[string]interface{}{
						"type":        "string",
						"description": "Market liquidity (low, normal, high)",
						"example":     "normal",
					},
				},
			},
			"insights": map[string]interface{}{
				"type":        "array",
				"description": "Key market insights and observations",
				"items": map[string]interface{}{
					"type": "string",
				},
				"example": []string{
					"Market is in uptrend phase",
					"Volatility is low",
				},
			},
		},
	}
}
