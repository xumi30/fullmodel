package sqlmemory

import (
	"database/sql"
	"fmt"
	"github.com/xumi30/fullmodel/utils"
	"github.com/xumi30/fullmodel/utils/logging"

	"time"
)

const dialogtable = `CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    messageID TEXT NOT NULL UNIQUE,
    chatID TEXT NOT NULL,
    agentID TEXT,
    role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'system', 'reasoning')),
    content TEXT NOT NULL,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (chatID) REFERENCES conversations(id) ON DELETE CASCADE
)`

const dialogindexchatID = `CREATE INDEX IF NOT EXISTS idx_messages_chatID ON messages(chatID)`
const dialogindexchatIDRole = `CREATE INDEX IF NOT EXISTS idx_messages_chatID_role ON messages(chatID, role)`
const dialogindextimestamp = `CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp DESC)`
const dialogindexchatIDTimestamp = `CREATE INDEX IF NOT EXISTS idx_messages_chatID_timestamp ON messages(chatID, timestamp DESC, id DESC)`

const subchattable = `CREATE TABLE IF NOT EXISTS chat_subchat (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chatID TEXT NOT NULL,
    subChatID TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (chatID) REFERENCES conversations(id) ON DELETE CASCADE
)`

const chatIDindex = `CREATE INDEX IF NOT EXISTS idx_chat_subchat_chatID ON chat_subchat(chatID)`
const subChatIDindex = `CREATE INDEX IF NOT EXISTS idx_chat_subchat_subChatID ON chat_subchat(subChatID)`

func (m *SQLMemory) InsertChatSubChat(chatID, subChatID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `INSERT INTO chat_subchat (chatID, subChatID) VALUES (?, ?)`
	_, err := m.db.Exec(query, chatID, subChatID)
	if err != nil {
		return fmt.Errorf("failed to insert chat-subchat relation: %w", err)
	}

	return nil
}

func (m *SQLMemory) GenerateSubChatIDWithChatId(ChatID string) (string, error) {
	subchatId := fmt.Sprintf("sub-%s", utils.GenerateChatID())
	if err := m.InsertChatSubChat(ChatID, subchatId); err != nil {
		logging.Error("插入子对话ID失败: %v", err)
		return "", err
	}
	logging.Info("生成子对话ID: %s, 归属对话ID: %s", subchatId, ChatID)
	return subchatId, nil

}

// GeneratePlanRunIDWithChatID 为一次「计划执行实例」生成隔离用 ID（用于 memory/system prompt 等临时上下文）。
// 注意：该 ID 不会创建新的 conversation，只会记录归属关系，便于调试/追踪。
func (m *SQLMemory) GeneratePlanRunIDWithChatID(chatID string) (string, error) {
	planRunID := fmt.Sprintf("planrun-%s", utils.GenerateChatID())
	if err := m.InsertChatSubChat(chatID, planRunID); err != nil {
		logging.Error("插入 planRunID 失败: %v", err)
		return "", err
	}
	logging.Info("生成 planRunID: %s, 归属对话ID: %s", planRunID, chatID)
	return planRunID, nil
}

// GetChatSubChat 获取子对话的归属关系
func (m *SQLMemory) GetChatSubChat(subChatID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT chatID FROM chat_subchat WHERE subChatID = ?`
	var chatID string
	err := m.db.QueryRow(query, subChatID).Scan(&chatID)
	if err != nil {
		return "", fmt.Errorf("failed to get chat-subchat relation: %w", err)
	}
	return chatID, nil
}

// SaveConversation 保存或更新对话

// SaveMessage 保存消息（写入时刻作为 timestamp）
func (m *SQLMemory) SaveMessage(chatID, messageID, agentID, role, content string) error {
	return m.SaveMessageWithTimestamp(chatID, messageID, agentID, role, content, time.Now(), 0)
}

// SaveMessageWithTimestamp 保存消息并指定 timestamp（用于流式首包到达时间，保证会话内排序正确）
func (m *SQLMemory) SaveMessageWithTimestamp(chatID, messageID, agentID, role, content string, ts time.Time, totalTokens int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ts.IsZero() {
		ts = time.Now()
	}
	if totalTokens < 0 {
		totalTokens = 0
	}

	query := `
		INSERT INTO messages (chatID, messageID, agentID, role, content, timestamp, total_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	_, err := m.db.Exec(query, chatID, messageID, agentID, role, content, ts.UTC(), totalTokens)
	if err != nil {
		return fmt.Errorf("failed to save message: %w", err)
	}
	if totalTokens > 0 {
		logging.Info("messages 入库 total_tokens chatID=%s messageID=%s role=%s tokens=%d", chatID, messageID, role, totalTokens)
	}

	return nil
}

func (m *SQLMemory) GetReasoningMessage(chatID string) ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT chatID, messageID, agentID, role, content, timestamp FROM messages WHERE chatID = ? AND role = 'reasoning' ORDER BY timestamp DESC`

	rows, err := m.db.Query(query, chatID)
	if err != nil {
		return nil, fmt.Errorf("failed to get reasoning message: %w", err)
	}
	defer rows.Close()

	var messages []map[string]interface{}

	for rows.Next() {
		var chatID string
		var messageID string
		var agentID sql.NullString
		var role, content string
		var timestamp time.Time

		if err := rows.Scan(&chatID, &messageID, &agentID, &role, &content, &timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, map[string]interface{}{
			"chatID":    chatID,
			"messageID": messageID,
			"agentID":   agentID.String,
			"role":      role,
			"content":   content,
			"timestamp": timestamp,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reasoning messages: %w", err)
	}

	return messages, nil
}

// GetMessagesByChatIDAndRole 按 chatID 与 role 获取消息列表（时间升序，字段与 GetMessages 一致）
func (m *SQLMemory) GetMessagesByChatIDAndRole(chatID, role string) ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT chatID, messageID, agentID, role, content, timestamp, IFNULL(total_tokens, 0) FROM messages WHERE chatID = ? AND role = ? ORDER BY timestamp ASC`

	rows, err := m.db.Query(query, chatID, role)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages by role: %w", err)
	}
	defer rows.Close()

	var messages []map[string]interface{}

	for rows.Next() {
		var cid string
		var messageID string
		var agentID sql.NullString
		var r, content string
		var timestamp time.Time
		var totalTok int

		if err := rows.Scan(&cid, &messageID, &agentID, &r, &content, &timestamp, &totalTok); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, map[string]interface{}{
			"chatID":       cid,
			"messageID":    messageID,
			"agentID":      agentID.String,
			"role":         r,
			"content":      content,
			"timestamp":    timestamp,
			"total_tokens": totalTok,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating messages by role: %w", err)
	}

	return messages, nil
}

// GetMessages 获取对话的所有消息
func (m *SQLMemory) GetMessages(chatID string) ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT chatID, messageID, agentID, role, content, timestamp, IFNULL(total_tokens, 0) FROM messages WHERE chatID = ? AND role != 'reasoning' ORDER BY timestamp ASC`
	rows, err := m.db.Query(query, chatID)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	defer rows.Close()

	var messages []map[string]interface{}

	for rows.Next() {
		var chatID string
		var messageID string
		var agentID sql.NullString
		var role, content string
		var timestamp time.Time
		var totalTok int

		if err := rows.Scan(&chatID, &messageID, &agentID, &role, &content, &timestamp, &totalTok); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		messages = append(messages, map[string]interface{}{
			"chatID":       chatID,
			"messageID":    messageID,
			"agentID":      agentID.String,
			"role":         role,
			"content":      content,
			"timestamp":    timestamp,
			"total_tokens": totalTok,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating messages: %w", err)
	}

	return messages, nil
}

// GetMessagesPage returns one page of non-reasoning messages. It reads from the
// newest end of the conversation and returns the page in chronological order.
func (m *SQLMemory) GetMessagesPage(chatID string, limit, offset int) ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 80
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT chatID, messageID, agentID, role, content, timestamp, IFNULL(total_tokens, 0)
		FROM (
			SELECT chatID, messageID, agentID, role, content, timestamp, total_tokens
			FROM messages
			WHERE chatID = ? AND role != 'reasoning'
			ORDER BY timestamp DESC, id DESC
			LIMIT ? OFFSET ?
		)
		ORDER BY timestamp ASC
	`
	rows, err := m.db.Query(query, chatID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get message page: %w", err)
	}
	defer rows.Close()

	var messages []map[string]interface{}
	for rows.Next() {
		var cid string
		var messageID string
		var agentID sql.NullString
		var role, content string
		var timestamp time.Time
		var totalTok int

		if err := rows.Scan(&cid, &messageID, &agentID, &role, &content, &timestamp, &totalTok); err != nil {
			return nil, fmt.Errorf("failed to scan message page: %w", err)
		}

		messages = append(messages, map[string]interface{}{
			"chatID":       cid,
			"messageID":    messageID,
			"agentID":      agentID.String,
			"role":         role,
			"content":      content,
			"timestamp":    timestamp,
			"total_tokens": totalTok,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating message page: %w", err)
	}

	return messages, nil
}

func (m *SQLMemory) GetMessagesByMessageID(messageID string) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT chatID, messageID, agentID, role, content, timestamp, IFNULL(total_tokens, 0) FROM messages WHERE messageID = ?`

	row := m.db.QueryRow(query, messageID)

	var chatID string
	var msgID string
	var agentID sql.NullString
	var role, content string
	var timestamp time.Time
	var totalTok int

	if err := row.Scan(&chatID, &msgID, &agentID, &role, &content, &timestamp, &totalTok); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message not found")
		}
		return nil, fmt.Errorf("failed to scan message: %w", err)
	}

	return map[string]interface{}{
		"chatID":       chatID,
		"messageID":    msgID,
		"agentID":      agentID.String,
		"role":         role,
		"content":      content,
		"timestamp":    timestamp,
		"total_tokens": totalTok,
	}, nil
}

// GetLastMessage 获取对话的最后一条消息
func (m *SQLMemory) GetLastMessage(chatid string) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `
		SELECT id, chatID, messageID, agentID, role, content, timestamp 
		FROM messages 
		WHERE chatID = ? 
		ORDER BY timestamp DESC 
		LIMIT 1
	`

	row := m.db.QueryRow(query, chatid)

	var id int
	var chatID, messageID string
	var agentID sql.NullString
	var role, content string
	var timestamp time.Time

	if err := row.Scan(&id, &chatID, &messageID, &agentID, &role, &content, &timestamp); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no messages found")
		}
		return nil, fmt.Errorf("failed to get last message: %w", err)
	}

	return map[string]interface{}{
		"id":        id,
		"chatID":    chatID,
		"messageID": messageID,
		"agentID":   agentID.String,
		"role":      role,
		"content":   content,
		"timestamp": timestamp,
	}, nil
}

// SelectAllMessageContents returns every stored message body (for doc path harvest). Excludes reasoning role.
func (m *SQLMemory) SelectAllMessageContents() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rows, err := m.db.Query(`SELECT content FROM messages WHERE role != 'reasoning' ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query message contents: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, fmt.Errorf("failed to scan message content: %w", err)
		}
		out = append(out, content)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating message contents: %w", err)
	}
	return out, nil
}

func (m *SQLMemory) DelateMessage(chatID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `DELETE FROM messages WHERE chatID = ?`

	_, err := m.db.Exec(query, chatID)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	return nil

}

func (m *SQLMemory) DeleteMessageByMessageID(messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, err := m.db.Exec(`DELETE FROM messages WHERE messageID = ?`, messageID)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("message not found")
	}
	return nil
}
