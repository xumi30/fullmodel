package crontab

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/xumi30/fullmodel/agent/tools"
	"github.com/xumi30/fullmodel/utils"
	"strings"
	"time"
)

type ListScheduledTasksTool struct{}

func NewListScheduledTasksTool() tools.Tool { return &ListScheduledTasksTool{} }

func (t *ListScheduledTasksTool) Name() string { return "list_scheduled_tasks" }

func (t *ListScheduledTasksTool) Description() string {
	return "List scheduled tasks with optional filtering by user_id and status. By default excludes deleted tasks."
}

func (t *ListScheduledTasksTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"user_id": map[string]interface{}{"type": "string"},
			"status": map[string]interface{}{
				"type": "string",
				"enum": []string{"active", "paused", "deleted"},
			},
			"include_deleted": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether to include deleted tasks when status is empty. Default false.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Max number of tasks to return. Default 50.",
			},
			"offset": map[string]interface{}{
				"type":        "integer",
				"description": "Offset for pagination. Default 0.",
			},
		},
	}
}

func (t *ListScheduledTasksTool) Execute(ctx context.Context, args string) (string, error) {
	var in struct {
		UserID         string `json:"user_id"`
		Status         string `json:"status"`
		IncludeDeleted bool   `json:"include_deleted"`
		Limit          int    `json:"limit"`
		Offset         int    `json:"offset"`
	}
	if strings.TrimSpace(args) != "" {
		if err := utils.UnmarshalLLMJSON(args, &in); err != nil {
			return "", fmt.Errorf("invalid JSON args: %w", err)
		}
	}

	in.UserID = strings.TrimSpace(in.UserID)
	in.Status = strings.TrimSpace(in.Status)
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	if in.Offset < 0 {
		in.Offset = 0
	}
	if err := validateEnums("", "", in.Status); err != nil {
		return "", err
	}

	db, err := openDB()
	if err != nil {
		return "", err
	}

	where := make([]string, 0, 3)
	params := make([]interface{}, 0, 4)

	if in.UserID != "" {
		where = append(where, "user_id = ?")
		params = append(params, in.UserID)
	}

	if in.Status != "" {
		where = append(where, "status = ?")
		params = append(params, in.Status)
	} else if !in.IncludeDeleted {
		where = append(where, "status != 'deleted'")
	}

	query := `
		SELECT id, user_id, title, action_type, action_payload, schedule_type, run_at, cron_expr, rrule, timezone, status, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_task`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	params = append(params, in.Limit, in.Offset)

	rows, err := db.Query(query, params...)
	if err != nil {
		return "", fmt.Errorf("list scheduled_task: %w", err)
	}
	defer rows.Close()

	tasks := make([]ScheduledTask, 0)
	for rows.Next() {
		var (
			id, userID, title, actionType, actionPayload, scheduleType, timezone, status string
			runAt                                                                        sql.NullTime
			cronExpr                                                                     sql.NullString
			rruleText                                                                    sql.NullString
			lastRunAt, nextRunAt                                                         sql.NullTime
			createdAt, updatedAt                                                         time.Time
		)
		if err := rows.Scan(&id, &userID, &title, &actionType, &actionPayload, &scheduleType, &runAt, &cronExpr, &rruleText, &timezone, &status, &lastRunAt, &nextRunAt, &createdAt, &updatedAt); err != nil {
			return "", fmt.Errorf("scan scheduled_task: %w", err)
		}
		tasks = append(tasks, taskFromRow(
			id, userID, title, actionType, actionPayload, scheduleType,
			runAt, cronExpr, rruleText, timezone, status, lastRunAt, nextRunAt,
			createdAt, updatedAt,
		))
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate scheduled_task: %w", err)
	}

	return toJSON(map[string]interface{}{
		"success": true,
		"count":   len(tasks),
		"tasks":   tasks,
	})
}

func (t *ListScheduledTasksTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "List scheduled tasks result.",
		"properties": map[string]interface{}{
			"success": map[string]interface{}{"type": "boolean"},
			"count":   map[string]interface{}{"type": "integer"},
			"tasks":   map[string]interface{}{"type": "array"},
		},
	}
}

func (t *ListScheduledTasksTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicCrontab, "列出已创建的定时任务（可按 status 过滤，默认不含 deleted）。")
}
