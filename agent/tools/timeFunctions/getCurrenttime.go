package timeFunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"fullmodel/agent/tools"
	"fullmodel/utils"

	"time"
)

// TimeTool implements the Tool interface for getting the current time
type CurrentTimeTool struct{}

func NewCurrentTimeTool() tools.Tool {
	return &CurrentTimeTool{}
}

// Name returns the name of the tool
func (t *CurrentTimeTool) Name() string {
	return "get_current_time"
}

// Description returns a description of what the tool does
func (t *CurrentTimeTool) Description() string {
	return "Tool for getting the current real-time date and time from the server. " +
		"MUST use this tool when user asks about current time, today's date, " +
		"or any time-related questions requiring real-time information. " +
		"Returns date, time, weekday, lunar date, and holiday information." +
		"只要对话里涉及时间的要应该先调用这个接口获取当前时间，再根据用户需求进行回复"
}

// Parameters returns the parameters that the tool accepts
func (t *CurrentTimeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

// Run executes the tool with the given input
func (t *CurrentTimeTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}
func (t *CurrentTimeTool) Execute(ctx context.Context, args string) (string, error) {
	now := time.Now()

	// 获取星期几
	weekdays := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	weekday := weekdays[now.Weekday()]

	// 获取节日信息
	holiday := getHoliday(now)

	// 构建结构化的时间信息
	result := map[string]interface{}{
		"current_time": now.Format("2006-01-02 15:04:05"),
		"weekday":      weekday,
		"lunar_date":   getLunarDate(now),
	}

	if holiday != "" {
		result["holiday"] = holiday
	}

	// 将结果序列化为JSON
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %v", err)
	}

	return string(jsonBytes), nil
}

func (t *CurrentTimeTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicTime, "获取服务器当前日期时间、星期、农历与节假日等实时时间信息。")
}

func (t *CurrentTimeTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Current time information including date, weekday, lunar date, and holiday",
		"properties": map[string]interface{}{
			"current_time": map[string]interface{}{
				"type":        "string",
				"description": "Current date and time in format 'YYYY-MM-DD HH:MM:SS'",
				"example":     "2024-01-15 10:30:45",
			},
			"weekday": map[string]interface{}{
				"type":        "string",
				"description": "Day of the week in Chinese",
				"example":     "星期一",
			},
			"lunar_date": map[string]interface{}{
				"type":        "string",
				"description": "Chinese lunar calendar date",
				"example":     "腊月初五",
			},
			"holiday": map[string]interface{}{
				"type":        "string",
				"description": "Holiday information (only present if it's a holiday)",
				"example":     "春节",
			},
		},
	}
}
