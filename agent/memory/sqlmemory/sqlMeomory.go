package sqlmemory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// SQLMemory 使用 SQLite 持久化存储对话历史
type SQLMemory struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

// 添加单例相关变量和函数
var (
	instance *SQLMemory
	once     sync.Once
)

// GetInstance 获取 SQLMemory 单例实例
func GetSqlInstance(dbPath string) (*SQLMemory, error) {
	if dbPath == "" {
		dbPath = "data/sqlmemory.db"
	}
	var err error
	once.Do(func() {
		instance, err = NewSQLMemory(dbPath)
	})
	return instance, err
}

// GetExistingInstance 获取已存在的 SQLMemory 单例实例，如果不存在则返回 nil
func GetExistingInstance() *SQLMemory {
	return instance
}

// ResetInstance 重置单例实例（主要用于测试）
func ResetInstance() {
	instance = nil
	once = sync.Once{}
}

// NewSQLMemory 创建一个新的 SQLMemory 实例
func NewSQLMemory(dbPath string) (*SQLMemory, error) {
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// 打开数据库连接
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 创建表结构
	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return &SQLMemory{
		db:     db,
		dbPath: dbPath,
	}, nil
}

// createTables 创建数据库表结构
func createTables(db *sql.DB) error {
	queries := []string{
		conversationstable,
		dialogtable,
		dialogindexchatID,
		dialogindexchatIDRole,
		dialogindextimestamp,
		dialogindexchatIDTimestamp,
		subchattable,
		chatIDindex,
		planstable,
		planstepstable,
		agentstable,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN total_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
		errStr := strings.ToLower(err.Error())
		if !strings.Contains(errStr, "duplicate column") {
			return fmt.Errorf("migrate messages add total_tokens: %w", err)
		}
	}

	if err := ensureConversationSchema(db); err != nil {
		return err
	}
	if err := ensureAgentsSchema(db); err != nil {
		return err
	}
	if err := ensurePresetAgents(db); err != nil {
		return err
	}

	return nil
}

func listTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return cols, rows.Err()
}

func ensureConversationSchema(db *sql.DB) error {
	cols, err := listTableColumns(db, "conversations")
	if err != nil {
		return fmt.Errorf("inspect conversations schema: %w", err)
	}
	if cols["agents"] {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE conversations ADD COLUMN agents TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return fmt.Errorf("migrate conversations add agents: %w", err)
	}
	return nil
}

// Close 关闭数据库连接
func (m *SQLMemory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		return m.db.Close()
	}

	return nil
}

// Backup 备份数据库
func (m *SQLMemory) Backup(backupPath string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 确保备份目录存在
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// 获取当前数据库路径
	currentDB, err := os.ReadFile(m.dbPath)
	if err != nil {
		return fmt.Errorf("failed to read database file: %w", err)
	}

	// 写入备份文件
	if err := os.WriteFile(backupPath, currentDB, 0644); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	return nil
}

// Restore 从备份恢复数据库
func (m *SQLMemory) Restore(backupPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查备份文件是否存在
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file does not exist: %w", err)
	}

	// 关闭当前数据库连接
	if m.db != nil {
		if err := m.db.Close(); err != nil {
			return fmt.Errorf("failed to close database: %w", err)
		}
	}

	// 读取备份文件
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	// 写入当前数据库路径
	if err := os.WriteFile(m.dbPath, backupData, 0644); err != nil {
		return fmt.Errorf("failed to restore database: %w", err)
	}

	// 重新打开数据库连接
	db, err := sql.Open("sqlite", m.dbPath)
	if err != nil {
		return fmt.Errorf("failed to reopen database: %w", err)
	}

	m.db = db
	return nil
}

// Export 导出对话数据为 JSON 格式
func (m *SQLMemory) Export(chatID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 获取对话信息
	conversation, err := m.GetConversation(chatID)
	if err != nil {
		return "", fmt.Errorf("failed to get conversation: %w", err)
	}

	// 获取对话消息
	messages, err := m.GetMessages(chatID)
	if err != nil {
		return "", fmt.Errorf("failed to get messages: %w", err)
	}

	// 构建导出数据
	exportData := map[string]interface{}{
		"conversation": conversation,
		"messages":     messages,
	}

	// 转换为 JSON
	jsonData, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal export data: %w", err)
	}

	return string(jsonData), nil
}

// Import 从 JSON 格式导入对话数据
func (m *SQLMemory) Import(jsonData string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 解析 JSON 数据
	var importData struct {
		Conversation map[string]interface{}   `json:"conversation"`
		Messages     []map[string]interface{} `json:"messages"`
	}

	if err := json.Unmarshal([]byte(jsonData), &importData); err != nil {
		return fmt.Errorf("failed to unmarshal import data: %w", err)
	}

	// 提取对话信息
	chatID, ok := importData.Conversation["id"].(string)
	if !ok {
		return fmt.Errorf("invalid conversation ID")
	}

	title, ok := importData.Conversation["title"].(string)
	if !ok {
		return fmt.Errorf("invalid conversation title")
	}

	mode, ok := importData.Conversation["mode"].(string)
	if !ok {
		mode = "chat"
	}

	// 保存对话
	if err := m.SaveConversation(chatID, title, mode); err != nil {
		return fmt.Errorf("failed to save conversation: %w", err)
	}

	// 保存消息
	for _, msg := range importData.Messages {
		role, ok := msg["role"].(string)
		if !ok {
			return fmt.Errorf("invalid message role")
		}

		content, ok := msg["content"].(string)
		if !ok {
			return fmt.Errorf("invalid message content")
		}
		MessageID, ok := msg["id"].(string)
		if !ok {
			return fmt.Errorf("invalid message id")
		}

		if err := m.SaveMessage(chatID, MessageID, "", role, content); err != nil {
			return fmt.Errorf("failed to save message: %w", err)
		}
	}

	return nil
}

// GetStats 获取数据库统计信息
func (m *SQLMemory) GetStats() (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make(map[string]interface{})

	// 获取对话数量
	var conversationCount int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM conversations").Scan(&conversationCount); err != nil {
		return nil, fmt.Errorf("failed to get conversation count: %w", err)
	}
	stats["conversation_count"] = conversationCount

	// 获取消息数量
	var messageCount int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messageCount); err != nil {
		return nil, fmt.Errorf("failed to get message count: %w", err)
	}
	stats["message_count"] = messageCount

	// 获取数据库大小
	fileInfo, err := os.Stat(m.dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get database size: %w", err)
	}
	stats["db_size_bytes"] = fileInfo.Size()

	return stats, nil
}
