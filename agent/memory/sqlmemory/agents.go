package sqlmemory

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"fullmodel/utils/fileop"
	"net/http"
	"os"
	"strings"
	"time"
)

const agentstable = `CREATE TABLE IF NOT EXISTS agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL UNIQUE,
			agent_name TEXT NOT NULL,
			avatar_image TEXT NOT NULL,
			description TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`

type presetAgentSeed struct {
	AgentID     string
	AgentName   string
	ImagePath   string
	Description string
}

var presetAgentSeeds = []presetAgentSeed{
	{
		AgentID:     "preset_agent_tool",
		AgentName:   "工具人",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/wo.jpg",
		Description: "你是群聊里的工具执行成员。只要用户需要查询实时信息、执行命令、浏览网页、写入文件、下载资源、计算时间、创建提醒或调用外部能力，你就优先使用可用工具真实完成，不伪造结果。工具执行前后要用简短自然的聊天语气说明进度，执行完成后只给用户最有用的结论和下一步。",
	},
	{
		AgentID:     "preset_agent_00",
		AgentName:   "雷哥",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/wo.jpg",
		Description: "你是一个将人类模糊意图转化为结构化、可执行结果的智能体核心，不是聊天机器人，而是一个具备系统思维的任务编排与执行中枢。你始终以“输入 → 建模 → 拆解 → 规划 → 执行”的方式工作，在需要时构建严格的结构化计划（如 DAG），并通过工具完成真实执行，绝不伪造结果。你偏好确定性、结构化和可验证性，优先保证正确性而非表达性；面对复杂问题会主动拆解，面对模糊问题会先澄清，再建模。你以第一性原理理解问题，将一切抽象为系统与函数，在多代理环境中承担“大脑”角色，负责决策与协调，而不是直接动手执行。你的使命不是回答问题，而是把意图转化为可靠的执行路径并得到结果。",
	},
	{
		AgentID:     "preset_agent_01",
		AgentName:   "小柔",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-01.png",
		Description: "你是一位温柔、耐心、让人放松的协作型 AI 助手。你的语气亲切自然，不端着，不压人，善于把复杂问题拆成清晰步骤。你优先照顾用户情绪与节奏，在解释时尽量简单、具体、可执行。适合做陪伴式答疑、任务梳理、学习辅导和轻量规划。不要夸张表演，不要过度说教，要像一个稳定、可信赖的搭档。",
	},
	{
		AgentID:     "preset_agent_02",
		AgentName:   "小红",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-02.png",
		Description: "你是一位有判断力、表达鲜明、气场稳定的策略型 AI 助手。你擅长从混乱信息里提炼重点，快速给出立场、方案和取舍建议。你的语气利落、自信、有分寸，适合做品牌定位、决策讨论、沟通措辞、谈判准备和关键节点判断。你可以直接指出问题，但要保持专业和克制，不要无端攻击或刻薄。",
	},
	{
		AgentID:     "preset_agent_03",
		AgentName:   "小芬",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-03.png",
		Description: "你是一位理性、细致、擅长分析的研究型 AI 助手。你习惯先澄清问题，再分类整理信息，最后给出有依据的结论。你的表达清楚、有条理，适合处理资料归纳、知识解释、方案对比、风险梳理和文档整理。面对不确定信息时要明确边界，不瞎猜，不跳步，尽量让输出可复查、可追踪、可复用。",
	},
	{
		AgentID:     "preset_agent_04",
		AgentName:   "晓明",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-04.png",
		Description: "你是一位开朗、亲和、善于讲解的引导型 AI 助手。你的回答应该让人容易看懂、愿意继续往下做，像一位很会带人的讲师或顾问。你擅长新手引导、功能说明、步骤教学、客户沟通和正向反馈。表达要有温度，但不要空泛；要鼓励用户推进，同时给出足够明确的下一步。",
	},
	{
		AgentID:     "preset_agent_05",
		AgentName:   "晓染",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-05.png",
		Description: "你是一位想法活跃、节奏明快、富有感染力的创意型 AI 助手。你擅长脑暴、文案、命名、活动包装、内容策划和风格延展。你的表达可以更鲜活、更有画面感，也可以适度带一点俏皮和灵气，但不能失控、不能浮夸堆词。你要在创意与落地之间保持平衡，让灵感最终能变成可执行方案。",
	},
	{
		AgentID:     "preset_agent_06",
		AgentName:   "晓严",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-06.png",
		Description: "你是一位经验老到、要求严格、重视质量底线的资深审阅型 AI 助手。你的风格直接、稳重、少废话，擅长挑错、找漏洞、做技术评审和把关。你会优先指出高风险问题、隐藏假设和可能的后果，再给出修正建议。允许语气稍硬，但核心目标是帮助用户把事情做扎实，而不是单纯否定。",
	},
	{
		AgentID:     "preset_agent_07",
		AgentName:   "晓亮",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-07.png",
		Description: "你是一位带点顽皮感、想象力强、世界观丰沛的幻想型 AI 助手。你擅长角色设定、故事灵感、视觉概念、创作陪跑和不那么常规的点子生成。你的语言可以灵动一点、俏皮一点、带点戏剧感，但仍然要围绕用户目标服务。适合创意写作、IP 设定、游戏叙事、风格探索和脑洞扩展。",
	},
	{
		AgentID:     "preset_agent_08",
		AgentName:   "晓峰",
		ImagePath:   "ui/wechat-desktop/public/assets/preset-agents/preset-agent-08.png",
		Description: "你是一位果断、清醒、行动导向很强的推进型 AI 助手。你会主动整理优先级、推动决策、压缩模糊空间，并把讨论拉回结果。你的语气冷静、有锋芒，但不是咄咄逼人。适合做项目推进、产品判断、执行拆解、时间安排和关键事项落地。你要帮助用户尽快从想法进入行动。",
	},
}

func fileToDataURI(relPath string) (string, error) {
	fullPath := fileop.ResolvePath(relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("read preset image %s: %w", fullPath, err)
	}

	mime := http.DetectContentType(data)
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func ensureAgentsSchema(db *sql.DB) error {
	cols, err := listTableColumns(db, "agents")
	if err != nil {
		return fmt.Errorf("inspect agents schema: %w", err)
	}
	if cols["id"] && cols["agent_name"] {
		_, _ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_agent_id ON agents(agent_id)`)
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin agents migration: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agents_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL UNIQUE,
			agent_name TEXT NOT NULL,
			avatar_image TEXT NOT NULL,
			description TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create agents_v2: %w", err)
	}

	selectSQL := `
		INSERT INTO agents_v2 (agent_id, agent_name, avatar_image, description, created_at, updated_at)
		SELECT agent_id, agent_id, avatar_image, description, created_at, updated_at
		FROM agents
	`
	if cols["agent_name"] {
		selectSQL = `
			INSERT INTO agents_v2 (agent_id, agent_name, avatar_image, description, created_at, updated_at)
			SELECT agent_id, COALESCE(NULLIF(agent_name, ''), agent_id), avatar_image, description, created_at, updated_at
			FROM agents
		`
	}
	if _, err := tx.Exec(selectSQL); err != nil {
		return fmt.Errorf("copy agents to agents_v2: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE agents`); err != nil {
		return fmt.Errorf("drop old agents: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE agents_v2 RENAME TO agents`); err != nil {
		return fmt.Errorf("rename agents_v2: %w", err)
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_agent_id ON agents(agent_id)`); err != nil {
		return fmt.Errorf("create agents index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agents migration: %w", err)
	}
	return nil
}

func ensurePresetAgents(db *sql.DB) error {
	for _, seed := range presetAgentSeeds {
		avatarImage := strings.TrimSpace(seed.ImagePath)
		if dataURI, err := fileToDataURI(seed.ImagePath); err == nil && strings.TrimSpace(dataURI) != "" {
			avatarImage = dataURI
		}
		if avatarImage == "" {
			avatarImage = seed.AgentName
		}
		_, err := db.Exec(`
			INSERT INTO agents (agent_id, agent_name, avatar_image, description, updated_at)
			VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(agent_id) DO UPDATE SET
				agent_name = excluded.agent_name,
				avatar_image = excluded.avatar_image,
				description = excluded.description,
				updated_at = CURRENT_TIMESTAMP
		`, seed.AgentID, seed.AgentName, avatarImage, seed.Description)
		if err != nil {
			return fmt.Errorf("seed preset agent %s: %w", seed.AgentID, err)
		}
	}
	return nil
}

func (m *SQLMemory) saveAgentLocked(agentID, agentName, avatarImage, description string) error {
	query := `
		INSERT INTO agents (agent_id, agent_name, avatar_image, description, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(agent_id) DO UPDATE SET
			agent_name = excluded.agent_name,
			avatar_image = excluded.avatar_image,
			description = excluded.description,
			updated_at = CURRENT_TIMESTAMP
	`
	_, err := m.db.Exec(query, agentID, agentName, avatarImage, description)
	if err != nil {
		return fmt.Errorf("failed to save agent: %w", err)
	}
	return nil
}

func (m *SQLMemory) SaveAgent(agentID, avatarImage, description string) error {
	return m.SaveAgentWithName(agentID, agentID, avatarImage, description)
}

func (m *SQLMemory) SaveAgentWithName(agentID, agentName, avatarImage, description string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("agentID cannot be empty")
	}
	if strings.HasPrefix(strings.TrimSpace(agentID), "preset_agent_") {
		return fmt.Errorf("preset agents cannot be modified")
	}
	if strings.TrimSpace(agentName) == "" {
		return fmt.Errorf("agent name cannot be empty")
	}
	avatarImage = strings.TrimSpace(avatarImage)
	if avatarImage == "" {
		avatarImage = strings.TrimSpace(agentName)
	}
	description = strings.TrimSpace(description)

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveAgentLocked(agentID, agentName, avatarImage, description)
}

func (m *SQLMemory) GetAgent(agentID string) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	row := m.db.QueryRow(`SELECT id, agent_id, agent_name, avatar_image, description, created_at, updated_at FROM agents WHERE agent_id = ?`, agentID)

	var id int64
	var agentIDValue, agentName, avatarImage, description string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&id, &agentIDValue, &agentName, &avatarImage, &description, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found")
		}
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}

	return map[string]interface{}{
		"id":           id,
		"agent_id":     agentIDValue,
		"agent_name":   agentName,
		"avatar_image": avatarImage,
		"description":  description,
		"created_at":   createdAt,
		"updated_at":   updatedAt,
	}, nil
}

func (m *SQLMemory) ListAgents() ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rows, err := m.db.Query(`
		SELECT id, agent_id, agent_name, avatar_image, description, created_at, updated_at
		FROM agents
		ORDER BY
			CASE WHEN agent_id LIKE 'preset_agent_%' THEN 0 ELSE 1 END,
			id ASC,
			updated_at DESC,
			created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}
	defer rows.Close()

	items := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int64
		var agentIDValue, agentName, avatarImage, description string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &agentIDValue, &agentName, &avatarImage, &description, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan agent: %w", err)
		}
		items = append(items, map[string]interface{}{
			"id":           id,
			"agent_id":     agentIDValue,
			"agent_name":   agentName,
			"avatar_image": avatarImage,
			"description":  description,
			"created_at":   createdAt,
			"updated_at":   updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed while iterating agents: %w", err)
	}
	return items, nil
}

func (m *SQLMemory) ListAgentsByIDs(agentIDs []string) ([]map[string]interface{}, error) {
	all, err := m.ListAgents()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]map[string]interface{}, len(all))
	for _, item := range all {
		key := strings.TrimSpace(fmt.Sprintf("%v", item["agent_id"]))
		if key != "" {
			byID[key] = item
		}
	}
	out := make([]map[string]interface{}, 0, len(agentIDs))
	for _, id := range agentIDs {
		if item, ok := byID[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *SQLMemory) DeleteCustomAgent(agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("agentID cannot be empty")
	}
	if strings.HasPrefix(agentID, "preset_agent_") {
		return fmt.Errorf("preset agents cannot be deleted")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete agent tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id, agents FROM conversations`)
	if err != nil {
		return fmt.Errorf("query conversations agents: %w", err)
	}

	type convAgentsRow struct {
		id     string
		agents []string
	}
	var updates []convAgentsRow
	for rows.Next() {
		var cid, raw string
		if err := rows.Scan(&cid, &raw); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan conversations agents: %w", err)
		}
		var ids []string
		if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &ids); err != nil {
			ids = []string{}
		}
		next := make([]string, 0, len(ids))
		changed := false
		for _, id := range ids {
			if strings.TrimSpace(id) == agentID {
				changed = true
				continue
			}
			next = append(next, id)
		}
		if changed {
			updates = append(updates, convAgentsRow{id: cid, agents: next})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate conversations agents: %w", err)
	}
	_ = rows.Close()

	for _, item := range updates {
		if _, err := tx.Exec(`UPDATE conversations SET agents = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, marshalConversationAgents(item.agents), item.id); err != nil {
			return fmt.Errorf("update conversations agents for %s: %w", item.id, err)
		}
	}

	res, err := tx.Exec(`DELETE FROM agents WHERE agent_id = ?`, agentID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return fmt.Errorf("agent not found")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete agent tx: %w", err)
	}
	return nil
}
