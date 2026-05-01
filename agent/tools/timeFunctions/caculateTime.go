package timeFunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"fullmodel/utils"

	"strconv"
	"strings"
	"time"
)

// CalculateTimeTool implements the Tool interface for time calculation
type CalculateTimeTool struct{}

func NewCalculateTimeTool() *CalculateTimeTool {
	return &CalculateTimeTool{}
}

// Name returns the name of the tool
func (t *CalculateTimeTool) Name() string {
	return "calculate_time"
}

// Description returns a description of what the tool does
func (t *CalculateTimeTool) Description() string {
	return "Calculates time by adding or subtracting duration from current time. Args format: 'operation:duration' where operation is 'add' or 'subtract', and duration is like '1h30m' (hours and minutes)"
}

// Parameters returns the parameters that the tool accepts
func (t *CalculateTimeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"description": "The operation to perform: 'add' or 'subtract'",
			},
			"duration": map[string]interface{}{
				"type":        "string",
				"description": "The duration to add or subtract, e.g., '1h30m' for 1 hour and 30 minutes",
			},
		},
		"required": []string{"operation", "duration"},
	}
}

// Run executes the tool with the given input
func (t *CalculateTimeTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

// Execute executes the tool with the given arguments
// Execute executes the tool with the given arguments
func (t *CalculateTimeTool) Execute(ctx context.Context, args string) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid input format: %v", err)
	}

	op, ok := params["operation"].(string)
	if !ok || op == "" {
		return "", fmt.Errorf("operation parameter is required")
	}

	duration, ok := params["duration"].(string)
	if !ok || duration == "" {
		return "", fmt.Errorf("duration parameter is required")
	}

	// 处理天数单位
	var durationValue time.Duration
	if strings.HasSuffix(duration, "d") {
		daysStr := strings.TrimSuffix(duration, "d")
		days, err := strconv.Atoi(daysStr)
		if err != nil {
			return "", fmt.Errorf("invalid days format: %v", err)
		}
		durationValue = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		durationValue, err = time.ParseDuration(duration)
		if err != nil {
			return "", fmt.Errorf("invalid duration format: %v", err)
		}
	}

	// Get current time
	now := time.Now()
	// Calculate the new time based on operation
	var result time.Time
	switch op {
	case "add":
		result = now.Add(durationValue)
	case "subtract":
		result = now.Add(-durationValue)
	default:
		return "", fmt.Errorf("invalid operation: %s. Must be 'add' or 'subtract'", op)
	}

	// 构建结构化的结果
	resultMap := map[string]interface{}{
		"calculated_time": result.Format(time.RFC3339),
	}

	// 将结果序列化为JSON
	jsonBytes, err := json.MarshalIndent(resultMap, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %v", err)
	}

	return string(jsonBytes), nil
}

func (t *CalculateTimeTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicTime, "以当前时刻为基准按给定时长做加减，得到新的日期时间。")
}

func (t *CalculateTimeTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Calculated time result",
		"properties": map[string]interface{}{
			"calculated_time": map[string]interface{}{
				"type":        "string",
				"description": "The calculated time in RFC3339 format",
				"example":     "2024-01-15T10:30:00+08:00",
			},
		},
	}
}
