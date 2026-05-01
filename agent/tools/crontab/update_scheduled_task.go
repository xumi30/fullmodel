package crontab

import (
	"context"
	"database/sql"
	"fmt"
	"fullmodel/agent/tools"
	"fullmodel/utils"
	"strings"
	"time"
)

type UpdateScheduledTaskTool struct{}

func NewUpdateScheduledTaskTool() tools.Tool { return &UpdateScheduledTaskTool{} }

func (t *UpdateScheduledTaskTool) Name() string { return "update_scheduled_task" }

func (t *UpdateScheduledTaskTool) Description() string {
	return "Update an existing scheduled task fields (title, action, schedule, timezone, status) and recompute next_run_at when needed."
}

func (t *UpdateScheduledTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id":             map[string]interface{}{"type": "string", "description": "Task id."},
			"title":          map[string]interface{}{"type": "string"},
			"action_type":    map[string]interface{}{"type": "string", "enum": []string{"notify", "tool"}},
			"action_payload": map[string]interface{}{"type": "string"},
			"schedule": map[string]interface{}{
				"type":        "object",
				"description": "Schedule object. Supported: once({run_at,timezone}), cron({cron_expr,timezone}), hourly({interval,timezone}).",
				"properties": map[string]interface{}{
					"type":      map[string]interface{}{"type": "string"},
					"run_at":    map[string]interface{}{"type": "string"},
					"timezone":  map[string]interface{}{"type": "string"},
					"cron_expr": map[string]interface{}{"type": "string"},
					"interval":  map[string]interface{}{"type": "integer"},
				},
			},

			// legacy flat fields
			"schedule_type": map[string]interface{}{"type": "string", "enum": []string{"once", "recurring"}},
			"run_at":        map[string]interface{}{"type": "string", "description": "RFC3339 or 'YYYY-MM-DD HH:MM:SS'"},
			"cron_expr":     map[string]interface{}{"type": "string"},
			"timezone":      map[string]interface{}{"type": "string"},
			"status":        map[string]interface{}{"type": "string", "enum": []string{"active", "paused", "deleted"}},
		},
		"required": []string{"id"},
		"oneOf": []interface{}{
			map[string]interface{}{
				"required": []string{"schedule"},
				"properties": map[string]interface{}{
					"schedule": map[string]interface{}{
						"oneOf": []interface{}{
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type":     map[string]interface{}{"type": "string", "const": "once"},
									"run_at":   map[string]interface{}{"type": "string"},
									"timezone": map[string]interface{}{"type": "string"},
								},
								"required": []string{"type", "run_at"},
							},
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type":     map[string]interface{}{"type": "string", "const": "hourly"},
									"interval": map[string]interface{}{"type": "integer", "minimum": 1, "default": 1},
									"timezone": map[string]interface{}{"type": "string"},
								},
								"required": []string{"type"},
							},
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type":      map[string]interface{}{"type": "string", "const": "cron"},
									"cron_expr": map[string]interface{}{"type": "string"},
									"timezone":  map[string]interface{}{"type": "string"},
								},
								"required": []string{"type", "cron_expr"},
							},
						},
					},
				},
			},
			map[string]interface{}{
				"required": []string{"schedule_type"},
			},
			map[string]interface{}{
				// If user only updates title/action/status, allow no schedule fields.
			},
		},
	}
}

func (t *UpdateScheduledTaskTool) Execute(ctx context.Context, args string) (string, error) {
	var in struct {
		ID            string        `json:"id"`
		Title         string        `json:"title"`
		ActionType    string        `json:"action_type"`
		ActionPayload string        `json:"action_payload"`
		Schedule      scheduleInput `json:"schedule"`
		ScheduleType  string        `json:"schedule_type"`
		RunAt         string        `json:"run_at"`
		CronExpr      string        `json:"cron_expr"`
		RRule         string        `json:"rrule"`
		Timezone      string        `json:"timezone"`
		Status        string        `json:"status"`
	}
	if err := utils.UnmarshalLLMJSON(args, &in); err != nil {
		return "", fmt.Errorf("invalid JSON args: %w", err)
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	if err := validateEnums(strings.TrimSpace(in.ActionType), strings.TrimSpace(in.ScheduleType), strings.TrimSpace(in.Status)); err != nil {
		return "", err
	}

	db, err := openDB()
	if err != nil {
		return "", err
	}

	// Load existing
	row := db.QueryRow(`
		SELECT id, user_id, title, action_type, action_payload, schedule_type, run_at, cron_expr, rrule, timezone, status, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_task WHERE id = ? LIMIT 1`, in.ID)

	var (
		id, userID, title, actionType, actionPayload, scheduleType, timezone, status string
		runAt                                                                        sql.NullTime
		cronExpr                                                                     sql.NullString
		rruleText                                                                    sql.NullString
		lastRunAt, nextRunAt                                                         sql.NullTime
		createdAt, updatedAt                                                         time.Time
	)
	if err := row.Scan(&id, &userID, &title, &actionType, &actionPayload, &scheduleType, &runAt, &cronExpr, &rruleText, &timezone, &status, &lastRunAt, &nextRunAt, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("task not found: %s", in.ID)
		}
		return "", fmt.Errorf("query scheduled_task: %w", err)
	}

	// Apply updates
	if s := strings.TrimSpace(in.Title); s != "" {
		title = s
	}
	if s := strings.TrimSpace(in.ActionType); s != "" {
		actionType = s
	}
	if s := strings.TrimSpace(in.ActionPayload); s != "" {
		actionPayload = s
	}
	if s := strings.TrimSpace(in.ScheduleType); s != "" {
		scheduleType = s
	}
	if s := strings.TrimSpace(in.Status); s != "" {
		status = s
	}

	// Normalize action payload based on (possibly updated) actionType.
	// Only enforce/normalize when the user provides action_payload, or when action_type changes.
	if strings.TrimSpace(in.ActionPayload) != "" || strings.TrimSpace(in.ActionType) != "" {
		payload, err := normalizeActionPayload(actionType, actionPayload)
		if err != nil {
			return "", err
		}
		actionPayload = payload
	}

	loc, tz, err := loadLocation(firstNonEmpty(strings.TrimSpace(in.Timezone), timezone))
	if err != nil {
		return "", err
	}
	timezone = tz

	// Prefer nested schedule object if provided (new API).
	if strings.TrimSpace(in.Schedule.Type) != "" {
		derivedType, derivedRunAt, derivedCron, derivedRRule, derivedTZ, derivedLoc, err := adaptSchedule(time.Now(), in.Schedule, timezone)
		if err != nil {
			return "", err
		}
		scheduleType = derivedType
		timezone = derivedTZ
		loc = derivedLoc
		if scheduleType == "once" {
			runAt = sql.NullTime{Time: derivedRunAt.UTC(), Valid: !derivedRunAt.IsZero()}
			cronExpr = sql.NullString{}
			rruleText = sql.NullString{}
		} else {
			runAt = sql.NullTime{}
			if strings.TrimSpace(derivedCron) != "" {
				cronExpr = sql.NullString{String: strings.TrimSpace(derivedCron), Valid: true}
			} else {
				cronExpr = sql.NullString{}
			}
			if strings.TrimSpace(derivedRRule) != "" {
				rruleText = sql.NullString{String: strings.TrimSpace(derivedRRule), Valid: true}
			} else {
				rruleText = sql.NullString{}
			}
		}
	}

	// Legacy direct rrule field (optional)
	if strings.TrimSpace(in.RRule) != "" {
		rruleText = sql.NullString{String: strings.TrimSpace(in.RRule), Valid: true}
		scheduleType = "recurring"
		runAt = sql.NullTime{}
		cronExpr = sql.NullString{}
	}

	if scheduleType == "once" {
		if strings.TrimSpace(in.CronExpr) != "" {
			cronExpr = sql.NullString{}
		}
		if strings.TrimSpace(in.RRule) != "" {
			rruleText = sql.NullString{}
		}
		if strings.TrimSpace(in.RunAt) != "" {
			parsed, err := parseTimeFlexible(strings.TrimSpace(in.RunAt), loc)
			if err != nil {
				return "", err
			}
			runAt = sql.NullTime{Time: parsed.UTC(), Valid: true}
		}
	} else if scheduleType == "recurring" {
		if strings.TrimSpace(in.RunAt) != "" {
			runAt = sql.NullTime{}
		}
		if strings.TrimSpace(in.CronExpr) != "" {
			cronExpr = sql.NullString{String: strings.TrimSpace(in.CronExpr), Valid: true}
		}
		if strings.TrimSpace(in.RRule) != "" {
			rruleText = sql.NullString{String: strings.TrimSpace(in.RRule), Valid: true}
			cronExpr = sql.NullString{}
		}
	}

	// Recompute next_run_at if active
	now := time.Now()
	var next time.Time
	if status == "active" {
		var runAtVal time.Time
		if runAt.Valid {
			runAtVal = runAt.Time
		}
		var cronVal string
		if cronExpr.Valid {
			cronVal = cronExpr.String
		}
		if rruleText.Valid && strings.TrimSpace(rruleText.String) != "" {
			next, err = computeNextRunFromRRule(now, rruleText.String, loc)
		} else {
			next, err = computeNextRun(now, scheduleType, runAtVal, cronVal, loc)
		}
		if err != nil {
			return "", err
		}
		nextRunAt = sql.NullTime{Time: next.UTC(), Valid: true}
	} else {
		nextRunAt = sql.NullTime{}
	}

	updatedAt = now.UTC()

	_, err = db.Exec(`
		UPDATE scheduled_task SET
			title = ?,
			action_type = ?,
			action_payload = ?,
			schedule_type = ?,
			run_at = ?,
			cron_expr = ?,
			rrule = ?,
			timezone = ?,
			status = ?,
			next_run_at = ?,
			updated_at = ?
		WHERE id = ?`,
		title,
		actionType,
		actionPayload,
		scheduleType,
		nullTimeToIface(runAt),
		nullStringToIface(cronExpr),
		nullStringToIface(rruleText),
		timezone,
		status,
		nullTimeToIface(nextRunAt),
		updatedAt,
		id,
	)
	if err != nil {
		return "", fmt.Errorf("update scheduled_task: %w", err)
	}

	task := taskFromRow(id, userID, title, actionType, actionPayload, scheduleType, runAt, cronExpr, rruleText, timezone, status, lastRunAt, nextRunAt, createdAt, updatedAt)
	return toJSON(map[string]interface{}{
		"success": true,
		"task":    task,
	})
}

func parseTimeFlexible(raw string, loc *time.Location) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, loc); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation(time.RFC3339, raw, loc); err == nil {
		return parsed, nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q: must be RFC3339 or 'YYYY-MM-DD HH:MM:SS'", raw)
}

func nullTimeToIface(t sql.NullTime) interface{} {
	if !t.Valid || t.Time.IsZero() {
		return nil
	}
	return t.Time.UTC()
}

func nullStringToIface(s sql.NullString) interface{} {
	if !s.Valid || strings.TrimSpace(s.String) == "" {
		return nil
	}
	return strings.TrimSpace(s.String)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

func (t *UpdateScheduledTaskTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Updated scheduled task.",
		"properties": map[string]interface{}{
			"success": map[string]interface{}{"type": "boolean"},
			"task":    map[string]interface{}{"type": "object"},
		},
	}
}

func (t *UpdateScheduledTaskTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicCrontab, "更新定时任务（标题、动作、RRULE/cron/时间、时区、状态），并自动重算下一次触发时间。")
}
