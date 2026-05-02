package bashfunction

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/agent/tools"
	"github.com/xumi30/fullmodel/utils"

	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// BashTool implements the Tool interface for executing bash or cmd commands
type BashTool struct{}

func NewBashTool() tools.Tool {
	return &BashTool{}
}

// Name returns the name of the tool
func (t *BashTool) Name() string {
	return "execute_command"
}

// Description returns a description of what the tool does
func (t *BashTool) Description() string {
	systemType := runtime.GOOS

	return "Tool for executing local bash (Linux/Mac) or cmd (Windows) commands. " +
		"Use this tool when user needs to run system commands, check system status, " +
		"or perform local operations. " +
		"Returns command output, error messages, and exit code. " +
		"当用户需要执行系统命令、根据软件运行的系统类型，选择对应的系统命令。" +
		"使用这个工具时，务必确保输入的命令是安全的，并且不会对系统造成破坏。" +
		"系统的类型是:" + systemType
}

// Parameters returns the parameters that the tool accepts
func (t *BashTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The command to execute",
			},
		},
		"required": []string{"command"},
	}
}

// Run executes the tool with the given input
func (t *BashTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *BashTool) Execute(ctx context.Context, args string) (string, error) {
	// 解析输入参数
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %v", err)
	}

	var command string
	var ok bool

	// 尝试直接获取 command
	if cmd, found := params["command"]; found {
		if cmdStr, isString := cmd.(string); isString {
			command = cmdStr
			ok = true
		}
	}

	// 如果没有直接找到 command，尝试从 properties 中解析
	if !ok {
		if props, found := params["properties"]; found {
			if propsStr, isString := props.(string); isString {
				var propParams map[string]string
				if err := json.Unmarshal([]byte(propsStr), &propParams); err == nil {
					if cmd, found := propParams["command"]; found {
						command = cmd
						ok = true
					}
				}
			}
		}
	}

	if !ok {
		return "", fmt.Errorf("command parameter is required, got params: %+v", params)
	}

	// 验证命令安全性
	if err := validateCommand(command); err != nil {
		return "", fmt.Errorf("command validation failed: %v", err)
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", command)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", command)
	}

	// 执行命令并获取输出
	output, err := cmd.CombinedOutput()

	// 构建结果
	result := map[string]interface{}{
		"command": command,
		"output":  strings.TrimSpace(string(output)),
		"success": err == nil,
	}

	// 处理错误情况
	if err != nil {
		result["error"] = err.Error()
		// 尝试获取退出码
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			result["exit_code"] = exitCode
			// 如果退出码不为0，返回错误
			if exitCode != 0 {
				// 将结果序列化为JSON
				jsonBytes, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return "", fmt.Errorf("failed to marshal result: %v", err)
				}
				return string(jsonBytes), fmt.Errorf("command failed with exit code %d: %s", exitCode, strings.TrimSpace(string(output)))
			}
		}

	}

	// 将结果序列化为JSON
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %v", err)
	}

	return string(jsonBytes), nil
}

// 危险命令列表，包括可能造成系统破坏的命令
var dangerousCommands = []string{
	"rm -rf /",
	":(){:|:&};:",
	"mkfs",
	"dd if=/dev/zero",
	"shutdown",
	"reboot",
	"halt",
	"poweroff",
	"del /f /s /q",
	"rmdir /s /q",
}

var dangerousCommandPatterns = []struct {
	label   string
	pattern *regexp.Regexp
}{
	{
		label:   "format",
		pattern: regexp.MustCompile(`(?i)^\s*format(?:\s|$)`),
	},
}

// validateCommand 检查命令是否包含危险操作
func validateCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is empty")
	}

	segments, err := splitSafeCommandSegments(command)
	if err != nil {
		return err
	}
	for _, segment := range segments {
		if err := validateSingleCommand(segment); err != nil {
			return err
		}
	}

	return nil
}

func validateSingleCommand(command string) error {
	cmdLower := strings.ToLower(strings.TrimSpace(command))
	if cmdLower == "" {
		return fmt.Errorf("command segment is empty")
	}

	// 检查是否匹配危险命令
	for _, dangerous := range dangerousCommands {
		if strings.Contains(cmdLower, strings.ToLower(dangerous)) {
			return fmt.Errorf("command contains potentially dangerous operation: %s", dangerous)
		}
	}

	for _, rule := range dangerousCommandPatterns {
		if rule.pattern.MatchString(command) {
			return fmt.Errorf("command contains potentially dangerous operation: %s", rule.label)
		}
	}

	// 检查是否包含命令注入高风险字符。
	// `&&` 会在 splitSafeCommandSegments 中作为受控串联被允许。
	if strings.ContainsAny(cmdLower, "|;`$()<>") {
		return fmt.Errorf("command contains potentially dangerous characters for command injection")
	}
	if strings.Contains(cmdLower, "&") {
		return fmt.Errorf("command contains potentially dangerous characters for command injection")
	}

	return nil
}

func splitSafeCommandSegments(command string) ([]string, error) {
	parts := strings.Split(command, "&&")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		seg := strings.TrimSpace(part)
		if seg == "" {
			return nil, fmt.Errorf("command contains an empty chained segment")
		}
		if strings.Contains(seg, "||") || strings.Contains(seg, ";") {
			return nil, fmt.Errorf("command contains unsupported chained operators")
		}
		segments = append(segments, seg)
	}
	return segments, nil
}

func (t *BashTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Execution results of the command including output, error messages, and exit code",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The command that was executed",
				"example":     "ls -la",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Standard output and error from the command",
				"example":     "total 24\ndrwxr-xr-x 5 user group 4096 Jan 15 10:30 .",
			},
			"success": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the command executed successfully",
				"example":     true,
			},
			"error": map[string]interface{}{
				"type":        "string",
				"description": "Error message if the command failed (only present if there was an error)",
				"example":     "exit status 127",
			},
			"exit_code": map[string]interface{}{
				"type":        "integer",
				"description": "Exit code of the command (only present if the command failed)",
				"example":     127,
			},
		},
	}
}

func (t *BashTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicSystem, "在本机执行经校验的安全 shell/cmd 命令并返回标准输出与退出码。")
}
