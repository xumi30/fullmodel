package sqlmemory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/utils/logging"

	"strconv"
	"strings"
	"time"
)

const conversationstable = `CREATE TABLE IF NOT EXISTS conversations (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			mode TEXT DEFAULT 'chat',
			agents TEXT NOT NULL DEFAULT '[]'
		)`

func parseConversationAgents(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return []string{}
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func marshalConversationAgents(ids []string) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

func (m *SQLMemory) SaveConversation(chatID, title, mode string) error {

	//需要确保chatID是纯数字
	if _, err := strconv.Atoi(chatID); err != nil {
		logging.Error("conversation ID must be a numeric string: %v", err)
		return fmt.Errorf("conversation ID must be a numeric string")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	query := `
		INSERT INTO conversations (id, title, mode, agents, updated_at)
		VALUES (?, ?, ?, '[]', CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			mode = excluded.mode,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err := m.db.Exec(query, chatID, title, mode)
	if err != nil {
		return fmt.Errorf("failed to save conversation: %w", err)
	}

	return nil
}

// GetConversation 获取对话信息
func (m *SQLMemory) GetConversation(chatID string) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, title, created_at, updated_at, mode, agents FROM conversations WHERE id = ?`

	row := m.db.QueryRow(query, chatID)

	var id, title, mode, agentsRaw string
	var createdAt, updatedAt time.Time

	if err := row.Scan(&id, &title, &createdAt, &updatedAt, &mode, &agentsRaw); err != nil {
		if err == sql.ErrNoRows {
			logging.Warn("Co313nversation with ID %s not found", chatID)
			return nil, fmt.Errorf("conversation not found")
		}
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	return map[string]interface{}{
		"id":         id,
		"title":      title,
		"created_at": createdAt,
		"updated_at": updatedAt,
		"mode":       mode,
		"agents":     parseConversationAgents(agentsRaw),
	}, nil
}

// ListConversations 列出所有对话
func (m *SQLMemory) ListConversations() ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `
		SELECT
			c.id,
			c.title,
			c.created_at,
			c.updated_at,
			c.mode,
			c.agents,
			IFNULL(m.content, '') AS last_message
		FROM conversations c
		LEFT JOIN messages m ON m.id = (
			SELECT id
			FROM messages
			WHERE chatID = c.id AND role != 'reasoning'
			ORDER BY timestamp DESC, id DESC
			LIMIT 1
		)
		ORDER BY c.updated_at DESC
	`

	rows, err := m.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	defer rows.Close()

	var conversations []map[string]interface{}

	for rows.Next() {
		var id, title, mode, agentsRaw, lastMessage string
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&id, &title, &createdAt, &updatedAt, &mode, &agentsRaw, &lastMessage); err != nil {
			return nil, fmt.Errorf("failed to scan conversation: %w", err)
		}

		conversations = append(conversations, map[string]interface{}{
			"id":           id,
			"title":        title,
			"created_at":   createdAt,
			"updated_at":   updatedAt,
			"mode":         mode,
			"agents":       parseConversationAgents(agentsRaw),
			"last_message": lastMessage,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating conversations: %w", err)
	}

	return conversations, nil
}

// DeleteConversation 删除对话及其所有消息
func (m *SQLMemory) DeleteConversation(chatID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// SQLite 的外键约束会自动删除相关消息
	_, err := m.db.Exec("DELETE FROM conversations WHERE id = ?", chatID)
	if err != nil {
		return fmt.Errorf("failed to delete conversation: %w", err)
	}

	return nil
}

func (m *SQLMemory) UpdateConversationMode(chatID, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `
		UPDATE conversations 
		SET mode = ?, updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`

	_, err := m.db.Exec(query, mode, chatID)
	if err != nil {
		return fmt.Errorf("failed to update conversation mode: %w", err)
	}

	return nil
}

// UpdateConversationTitle 更新对话标题
func (m *SQLMemory) UpdateConversationTitle(chatID, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `
        UPDATE conversations
        SET title = ?, updated_at = CURRENT_TIMESTAMP
        WHERE id = ?
    `
	_, err := m.db.Exec(query, title, chatID)
	if err != nil {
		return fmt.Errorf("failed to update conversation title: %w", err)
	}
	return nil
}

func (m *SQLMemory) ReplaceConversationAgents(chatID string, agentIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `
		UPDATE conversations
		SET agents = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := m.db.Exec(query, marshalConversationAgents(agentIDs), chatID)
	if err != nil {
		return fmt.Errorf("failed to update conversation agents: %w", err)
	}
	return nil
}

func (m *SQLMemory) AddAgentToConversation(chatID, agentID string) error {
	conv, err := m.GetConversation(chatID)
	if err != nil {
		return err
	}
	rawIDs, _ := conv["agents"].([]string)
	next := append([]string{}, rawIDs...)
	for _, existing := range next {
		if existing == agentID {
			return nil
		}
	}
	next = append(next, agentID)
	return m.ReplaceConversationAgents(chatID, next)
}

// ListConversationAgents 根据chatID返回该对话关联的agent ID列表
func (m *SQLMemory) ListConversationAgents(chatID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT agents FROM conversations WHERE id = ?`

	var agentsRaw string
	err := m.db.QueryRow(query, chatID).Scan(&agentsRaw)
	if err != nil {
		if err == sql.ErrNoRows {
			logging.Warn("Conversation with ID %s not found", chatID)
			return nil, fmt.Errorf("conversation not found")
		}
		return nil, fmt.Errorf("failed to get conversation agents: %w", err)
	}

	return parseConversationAgents(agentsRaw), nil
}
