package searchFunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/utils"
	"github.com/xumi30/fullmodel/utils/logging"
	"io"

	"net/http"
	"time"
)

// WeatherTool implements the Tool interface for weather queries
type WeatherTool struct{}

// NewWeatherTool creates a new WeatherTool instance
func NewWeatherTool() *WeatherTool {
	return &WeatherTool{}
}

// Name returns the name of the tool
func (t *WeatherTool) Name() string {
	return "query_weather"
}

// Description returns a description of what the tool does
func (t *WeatherTool) Description() string {
	return "Get weather and forecast information for a specific location. " +
		"This tool provides comprehensive weather data including temperature, humidity, " +
		"wind speed, precipitation probability, and weather conditions. " +
		"Use this tool when user asks about weather, temperature, or forecast for any location. " +
		"Requires latitude and longitude coordinates of the location."
}

// Parameters returns the parameters that the tool accepts
func (t *WeatherTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"latitude": map[string]interface{}{
				"type":        "string",
				"description": "The latitude coordinate of the location (e.g., 39.9042 for Beijing)",
			},
			"longitude": map[string]interface{}{
				"type":        "string",
				"description": "The longitude coordinate of the location (e.g., 116.4074 for Beijing)",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "Number of days to forecast (default: 7, max: 16)",
			},
		},
		"required": []string{"latitude", "longitude"},
	}
}

// Run executes the tool with the given input
func (t *WeatherTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

// Execute executes the tool with the given arguments
func (t *WeatherTool) Execute(ctx context.Context, args string) (string, error) {
	args = utils.ExtractJSON(args)
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		logging.Error("Error parsing parameters: %v", err)
		return "", fmt.Errorf("failed to parse parameters: %v", err)
	}

	// 检查参数是否嵌套
	if latObj, ok := params["latitude"].(map[string]interface{}); ok {
		// 如果 latitude 是一个对象，尝试从中提取实际值
		if latVal, ok := latObj["latitude"].(string); ok {
			params["latitude"] = latVal
		}
		if lonVal, ok := latObj["longitude"].(string); ok {
			params["longitude"] = lonVal
		}
	}

	lat, ok := params["latitude"].(string)
	if !ok {
		return "", fmt.Errorf("latitude parameter is required and must be a string")
	}

	lon, ok := params["longitude"].(string)
	if !ok {
		return "", fmt.Errorf("longitude parameter is required and must be a string")
	}

	days := parseDays(params)

	// 更新API请求，获取更多天的数据
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s&hourly=temperature_2m,relative_humidity_2m,apparent_temperature,precipitation_probability,weather_code,wind_speed_10m&forecast_days=%d",
		lat, lon, days,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		logging.Error("Error creating HTTP request: %v", err)
		return "", fmt.Errorf("failed to create HTTP request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logging.Error("Error making HTTP request: %v", err)
		return "", fmt.Errorf("failed to make HTTP request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logging.Error("Error reading response body: %v", err)
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	return t.formatWeatherData(body, days)
}

func (t *WeatherTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicSearch, "根据经纬度查询当地实况与多日天气预报。")
}

func (t *WeatherTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Weather forecast data with daily breakdown and insights",
		"properties": map[string]interface{}{
			"forecast_days": map[string]interface{}{
				"type":        "integer",
				"description": "Number of days in the forecast (1-16)",
				"example":     3,
			},
			"overall_insights": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
				"description": "Overall weather trends and patterns for the forecast period",
				"example": []string{
					"Overall temperature trend: Warming up over the forecast period",
					"Rain expected on 2 day(s) during the forecast period",
				},
			},
			"daily_forecast": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"date": map[string]interface{}{
							"type":        "string",
							"description": "Date in YYYY-MM-DD format",
							"example":     "2024-01-15",
						},
						"summary": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"condition": map[string]interface{}{
									"type":        "string",
									"description": "Primary weather condition",
									"example":     "Partly cloudy",
								},
								"temperature_range": map[string]interface{}{
									"type":        "string",
									"description": "Temperature range in Celsius",
									"example":     "15.5°C ~ 22.3°C",
								},
								"trend": map[string]interface{}{
									"type":        "string",
									"description": "Temperature trend (warming/cooling/stable)",
									"example":     "warming",
								},
								"max_precip_prob": map[string]interface{}{
									"type":        "integer",
									"description": "Maximum precipitation probability (0-100)",
									"example":     30,
								},
								"max_wind_speed": map[string]interface{}{
									"type":        "number",
									"description": "Maximum wind speed in km/h",
									"example":     15.5,
								},
							},
						},
						"insights": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "string",
							},
							"description": "Daily weather insights and recommendations",
							"example": []string{
								"Temperature rising",
								"High chance of rain - consider carrying an umbrella",
							},
						},
					},
				},
				"description": "Array of daily weather forecasts",
			},
		},
	}
}

// WeatherResponse represents the API response structure
type WeatherResponse struct {
	Hourly struct {
		Time                     []string  `json:"time"`
		Temperature2M            []float64 `json:"temperature_2m"`
		RelativeHumidity2M       []int     `json:"relative_humidity_2m"`
		ApparentTemperature      []float64 `json:"apparent_temperature"`
		PrecipitationProbability []int     `json:"precipitation_probability"`
		WeatherCode              []int     `json:"weather_code"`
		WindSpeed10M             []float64 `json:"wind_speed_10m"`
	} `json:"hourly"`
}

func (t *WeatherTool) formatWeatherData(data []byte, days int) (string, error) {
	var weather WeatherResponse
	if err := json.Unmarshal(data, &weather); err != nil {
		return "", err
	}

	n := min(days*24, len(weather.Hourly.Time))
	if n == 0 {
		return "", fmt.Errorf("no data")
	}

	// 按天分组数据
	dailyData := make([]map[string]interface{}, 0, days)
	for day := 0; day < days && day*24 < n; day++ {
		startIdx := day * 24
		endIdx := min(startIdx+24, n)

		dayTemps := weather.Hourly.Temperature2M[startIdx:endIdx]
		dayRain := weather.Hourly.PrecipitationProbability[startIdx:endIdx]
		dayWind := weather.Hourly.WindSpeed10M[startIdx:endIdx]
		dayCodes := weather.Hourly.WeatherCode[startIdx:endIdx]

		// 计算每日统计信息
		minTemp, maxTemp := dayTemps[0], dayTemps[0]
		maxRain := dayRain[0]
		maxWind := dayWind[0]

		// 统计每日天气状况
		conditionCounts := make(map[string]int)

		for i := 0; i < len(dayTemps); i++ {
			if dayTemps[i] < minTemp {
				minTemp = dayTemps[i]
			}
			if dayTemps[i] > maxTemp {
				maxTemp = dayTemps[i]
			}
			if dayRain[i] > maxRain {
				maxRain = dayRain[i]
			}
			if dayWind[i] > maxWind {
				maxWind = dayWind[i]
			}

			condition := getWeatherCondition(dayCodes[i])
			conditionCounts[condition]++
		}

		// 确定主要天气状况
		mainCondition := "Mixed weather"
		maxCount := 0
		for condition, count := range conditionCounts {
			if count > maxCount {
				maxCount = count
				mainCondition = condition
			}
		}

		// 判断温度趋势
		trend := "stable"
		if len(dayTemps) > 12 {
			if dayTemps[len(dayTemps)-1] > dayTemps[0]+2 {
				trend = "warming"
			} else if dayTemps[len(dayTemps)-1] < dayTemps[0]-2 {
				trend = "cooling"
			}
		}

		// 判断风险
		rainRisk := maxRain > 50
		windRisk := maxWind > 30

		// 生成每日洞察
		dayInsights := []string{}
		if rainRisk {
			dayInsights = append(dayInsights, "🌧 High chance of rain — consider carrying an umbrella")
		}
		if windRisk {
			dayInsights = append(dayInsights, "💨 Strong winds expected")
		}
		if trend == "cooling" {
			dayInsights = append(dayInsights, "📉 Temperature dropping — consider warmer clothing")
		}
		if trend == "warming" {
			dayInsights = append(dayInsights, "📈 Temperature rising")
		}

		// 添加每日数据
		dayData := map[string]interface{}{
			"date": weather.Hourly.Time[startIdx][:10], // 提取日期部分
			"summary": map[string]interface{}{
				"condition":         mainCondition,
				"temperature_range": fmt.Sprintf("%.1f°C ~ %.1f°C", minTemp, maxTemp),
				"trend":             trend,
				"max_precip_prob":   maxRain,
				"max_wind_speed":    maxWind,
			},
			"insights": dayInsights,
		}

		dailyData = append(dailyData, dayData)
	}

	// 生成整体洞察
	overallInsights := []string{}

	// 计算整体温度趋势
	firstDayMinTemp := dailyData[0]["summary"].(map[string]interface{})["temperature_range"].(string)
	lastDayMinTemp := dailyData[len(dailyData)-1]["summary"].(map[string]interface{})["temperature_range"].(string)

	// 简单解析温度范围字符串
	var firstDayMin, lastDayMin float64
	fmt.Sscanf(firstDayMinTemp, "%f", &firstDayMin)
	fmt.Sscanf(lastDayMinTemp, "%f", &lastDayMin)

	if lastDayMin > firstDayMin+2 {
		overallInsights = append(overallInsights, "📈 Overall temperature trend: Warming up over the forecast period")
	} else if lastDayMin < firstDayMin-2 {
		overallInsights = append(overallInsights, "📉 Overall temperature trend: Cooling down over the forecast period")
	} else {
		overallInsights = append(overallInsights, "🌡️ Overall temperature trend: Stable over the forecast period")
	}

	// 检查是否有连续雨天
	rainyDays := 0
	for _, day := range dailyData {
		if day["summary"].(map[string]interface{})["max_precip_prob"].(int) > 50 {
			rainyDays++
		}
	}

	if rainyDays >= 3 {
		overallInsights = append(overallInsights, "🌧️ Extended rainy period expected — prepare for wet weather")
	} else if rainyDays > 0 {
		overallInsights = append(overallInsights, fmt.Sprintf("🌧️ Rain expected on %d day(s) during the forecast period", rainyDays))
	} else {
		overallInsights = append(overallInsights, "☀️ No significant rain expected during the forecast period")
	}

	// 结构化返回
	result := map[string]interface{}{
		"forecast_days":    days,
		"overall_insights": overallInsights,
		"daily_forecast":   dailyData,
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal weather data: %v", err)
	}
	return string(jsonBytes), nil
}

func parseDays(params map[string]interface{}) int {
	if d, ok := params["days"].(float64); ok {
		// 限制最大天数为16天（Open-Meteo API的限制）
		days := int(d)
		if days > 16 {
			days = 16
		}
		if days < 1 {
			days = 1
		}
		return days
	}
	return 7 // 默认7天
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getWeatherDescription converts WMO weather code to natural language
func getWeatherCondition(code int) string {
	descriptions := map[int]string{
		0:  "Clear sky",
		1:  "Mainly clear",
		2:  "Partly cloudy",
		3:  "Overcast",
		45: "Fog",
		48: "Depositing rime fog",
		51: "Light drizzle",
		53: "Moderate drizzle",
		55: "Dense drizzle",
		61: "Slight rain",
		63: "Moderate rain",
		65: "Heavy rain",
		71: "Slight snow",
		73: "Moderate snow",
		75: "Heavy snow",
		77: "Snow grains",
		80: "Slight rain showers",
		81: "Moderate rain showers",
		82: "Violent rain showers",
		85: "Slight snow showers",
		86: "Heavy snow showers",
		95: "Thunderstorm",
		96: "Thunderstorm with slight hail",
		99: "Thunderstorm with heavy hail",
	}

	if desc, ok := descriptions[code]; ok {
		return desc
	}
	return "Unknown weather condition"
}
