package memory

import (
	"encoding/json"
	"fmt"

	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/agent/tools"
	"github.com/xumi30/fullmodel/utils"
	"github.com/xumi30/fullmodel/utils/logging"
	"strings"
	"sync"
)

// Message 与 brain.Message 一致，便于本地记忆与 Chat API 共用同一结构。
type Message = brain.Message

const (
	MessageRoleUser      = "user"
	MessageRoleAssistant = "assistant"
	MessageRoleTool      = "tool"
)

// MessageContentString 返回用于 YAML/压缩/启发式判断的文本 content；纯字符串消息原样返回，其它类型序列化为 JSON。
func MessageContentString(m *Message) string {
	if m == nil {
		return ""
	}
	switch v := m.Content.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

type localMemory struct {
	Messages map[string][]*Message
	RwLock   sync.RWMutex

	// assistantReplyTurns 记录每会话「助手正文回复」次数，用于每满 AutoCompressEveryAssistantTurns 触发记忆压缩。
	assistantReplyTurns map[string]int
	compressTurnMu      sync.Mutex
}

var (
	localMemoryInstance *localMemory
	localMemoryOnce     sync.Once
)

// GetLocalMemory 获取全局单例 LocalMemory 对象
func GetLocalMemory() *localMemory {
	localMemoryOnce.Do(func() {
		localMemoryInstance = newLocalMemory()
	})
	return localMemoryInstance
}

func newLocalMemory() *localMemory {
	messages := make(map[string][]*Message)
	return &localMemory{
		Messages:            messages,
		assistantReplyTurns: make(map[string]int),
	}
}

func (m *localMemory) AddMessage(chatID string, message *Message) int {
	// logging.Info("AddMessage called for chatID: %s: %v", chatID, message)
	m.RwLock.Lock()
	defer m.RwLock.Unlock()
	if _, ok := m.Messages[chatID]; !ok {
		m.Messages[chatID] = []*Message{}
	}
	m.Messages[chatID] = append(m.Messages[chatID], message)
	return len(m.Messages[chatID]) - 1
}

func (m *localMemory) GetMessages(chatID string) []*Message {
	logging.Info("GetMessages called for chatID: %s", chatID)
	m.RwLock.RLock()
	defer m.RwLock.RUnlock()
	if msgs, ok := m.Messages[chatID]; ok {
		logging.Info("Messages found for chatID: %s", chatID)
		out := make([]*Message, 0, len(msgs))
		for _, msg := range msgs {
			out = append(out, cloneLocalMessage(msg))
		}
		return out
	}
	return []*Message{}
}

func cloneLocalMessage(msg *Message) *Message {
	if msg == nil {
		return nil
	}
	cp := *msg
	if len(msg.ToolCalls) > 0 {
		cp.ToolCalls = append([]brain.ToolCall(nil), msg.ToolCalls...)
	}
	switch c := msg.Content.(type) {
	case string:
		cp.Content = c
	case nil:
		cp.Content = nil
	default:
		cp.Content = msg.Content
	}
	return &cp
}

func (m *localMemory) Clear(chatID string) {
	m.RwLock.Lock()
	defer m.RwLock.Unlock()
	delete(m.Messages, chatID)
	m.compressTurnMu.Lock()
	delete(m.assistantReplyTurns, chatID)
	m.compressTurnMu.Unlock()
}

func (m *localMemory) DeleteMemoryByIndex(chatID string, index int) bool {
	m.RwLock.Lock()
	defer m.RwLock.Unlock()
	if msgs, ok := m.Messages[chatID]; ok {
		if index >= 0 && index < len(msgs) {
			m.Messages[chatID][index] = nil
			return true
		}
	}
	return false
}

func AddToolMessage(chatId, toolid string, toolMessage string) int {
	if utils.IsBlank(toolMessage) && utils.IsBlank(toolid) {
		return -1
	}
	memoryLocal := GetLocalMemory()
	toolResultMsg := Message{
		Role:       MessageRoleTool,
		ToolCallID: toolid,
		Content:    toolMessage,
	}
	return memoryLocal.AddMessage(chatId, &toolResultMsg)
}

func AddUserMessage(chatId, userMessage string) int {
	if utils.IsBlank(userMessage) {
		return -1
	}
	memoryLocal := GetLocalMemory()
	userMsg := Message{
		Role:    MessageRoleUser,
		Content: userMessage,
	}
	return memoryLocal.AddMessage(chatId, &userMsg)
}

func AddAssistantToolCallsMessage(chatId string, toolCalls []brain.ToolCall) int {
	if len(toolCalls) == 0 {
		return -1
	}
	memoryLocal := GetLocalMemory()
	assistantMsg := Message{
		Role:      MessageRoleAssistant,
		ToolCalls: toolCalls,
	}
	return memoryLocal.AddMessage(chatId, &assistantMsg)
}

func isInternalToolContinuationPrompt(content string) bool {
	s := strings.TrimSpace(content)
	return strings.HasPrefix(s, "工具已经执行完成") ||
		strings.HasPrefix(s, "已按你的要求加载")
}

func SetToolsInfo(chatId string) {
	toolRegistry := tools.Getregistry()
	js, err := toolRegistry.ConvertToolsToJSON()
	if err != nil {
		logging.Error("ConvertToolsToJSON failed: %v", err)

	}
	AddUserMessage(chatId, fmt.Sprintf("这些是你能使用的工具消息：\n%s", js))
}

func DeleteMemoryByIndex(chatId string, index int) bool {
	memoryLocal := GetLocalMemory()
	return memoryLocal.DeleteMemoryByIndex(chatId, index)
}
