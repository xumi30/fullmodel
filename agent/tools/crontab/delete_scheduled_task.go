package crontab

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fullmodel/agent/tools"
	"fullmodel/utils"
)

type DeleteScheduledTaskTool struct{}

func NewDeleteScheduledTaskTool() tools.Tool { return &DeleteScheduledTaskTool{} }

func (t *DeleteScheduledTaskTool) Name() string { return "delete_scheduled_task" }

func (t *DeleteScheduledTaskTool) Description() string {
	return "Soft-delete a scheduled task by setting status=deleted."
}

func (t *DeleteScheduledTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Task id.",
			},
		},
		"required": []string{"id"},
	}
}

func (t *DeleteScheduledTaskTool) Execute(ctx context.Context, args string) (string, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := utils.UnmarshalLLMJSON(args, &in); err != nil {
		return "", fmt.Errorf("invalid JSON args: %w", err)
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	db, err := openDB()
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	res, err := db.Exec(`UPDATE scheduled_task SET status='deleted', next_run_at=NULL, updated_at=? WHERE id=? AND status!='deleted'`, now, in.ID)
	if err != nil {
		return "", fmt.Errorf("delete scheduled_task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Could be already deleted or not found; keep message explicit.
		// We avoid leaking DB errors; respond idempotently.
		return toJSON(map[string]interface{}{
			"success": false,
			"message": "task not found or already deleted",
			"id":      in.ID,
		})
	}

	return toJSON(map[string]interface{}{
		"success": true,
		"id":      in.ID,
		"status":  "deleted",
	})
}

func (t *DeleteScheduledTaskTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Delete scheduled task result.",
		"properties": map[string]interface{}{
			"success": map[string]interface{}{"type": "boolean"},
			"id":      map[string]interface{}{"type": "string"},
			"status":  map[string]interface{}{"type": "string"},
			"message": map[string]interface{}{"type": "string"},
		},
	}
}

func (t *DeleteScheduledTaskTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicCrontab, "删除（软删除）指定的定时任务：将状态置为 deleted。")
}
