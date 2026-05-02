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

type CreateScheduledTaskTool struct{}

func NewCreateScheduledTaskTool() tools.Tool {
	return &CreateScheduledTaskTool{}
}

func (t *CreateScheduledTaskTool) Name() string { return "create_scheduled_task" }

func (t *CreateScheduledTaskTool) Description() string {
	return "Create a scheduled task (once or recurring cron). Stores task in local SQLite. ActionType: notify/tool; ScheduleType: once/recurring."
}

func (t *CreateScheduledTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type":        "string",
				"description": "Task title. Example: 投简历 / 喝水提醒",
			},
			"action_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"notify", "tool"},
				"description": "notify: send a reminder; tool: call a tool.",
			},
			"action_payload": map[string]interface{}{
				"type":        "string",
				"description": "notify: plain text to remind. tool: JSON string like {\"tool\":\"query_weather\",\"args\":{\"city\":\"Singapore\"}}",
			},
			"status": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"active", "paused"},
				"description": "Optional. Default active.",
				"default":     "active",
			},

			// New API
			"schedule": map[string]interface{}{
				"type":        "object",
				"description": "Preferred. Choose exactly one schedule type: once / hourly / rrule / cron.",
			},

			// Legacy API (still accepted)
			"schedule_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"once", "recurring"},
				"description": "Legacy: once uses run_at; recurring uses cron_expr.",
			},
			"run_at": map[string]interface{}{
				"type":        "string",
				"description": "Legacy for once: RFC3339 (recommended) or 'YYYY-MM-DD HH:MM:SS'.",
			},
			"cron_expr": map[string]interface{}{
				"type":        "string",
				"description": "Legacy for recurring: 5-field cron 'min hour dom mon dow'. Example: '0 */2 * * *'.",
			},
			"timezone": map[string]interface{}{
				"type":        "string",
				"description": "Legacy timezone when schedule.timezone not provided. Example: Asia/Singapore.",
			},
		},
		"required": []string{"title", "action_type", "action_payload"},
		"oneOf": []interface{}{
			// New schedule API
			map[string]interface{}{
				"required": []string{"schedule"},
				"properties": map[string]interface{}{
					"schedule": map[string]interface{}{
						"oneOf": []interface{}{
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type":     map[string]interface{}{"type": "string", "const": "once"},
									"run_at":   map[string]interface{}{"type": "string", "description": "RFC3339 time, e.g. 2026-04-21T09:00:00+08:00"},
									"timezone": map[string]interface{}{"type": "string", "description": "IANA timezone, e.g. Asia/Singapore"},
								},
								"required": []string{"type", "run_at"},
							},
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type":     map[string]interface{}{"type": "string", "const": "hourly"},
									"interval": map[string]interface{}{"type": "integer", "minimum": 1, "default": 1, "description": "Every N hours"},
									"timezone": map[string]interface{}{"type": "string", "description": "IANA timezone, e.g. Asia/Singapore"},
								},
								"required": []string{"type"},
							},
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type":     map[string]interface{}{"type": "string", "const": "rrule"},
									"rrule":    map[string]interface{}{"type": "string", "description": "RFC5545 RRULE, e.g. FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE,FR"},
									"timezone": map[string]interface{}{"type": "string", "description": "IANA timezone, e.g. Asia/Singapore"},
								},
								"required": []string{"type", "rrule"},
							},
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type":      map[string]interface{}{"type": "string", "const": "cron"},
									"cron_expr": map[string]interface{}{"type": "string", "description": "5-field cron expression"},
									"timezone":  map[string]interface{}{"type": "string", "description": "IANA timezone, e.g. Asia/Singapore"},
								},
								"required": []string{"type", "cron_expr"},
							},
						},
					},
				},
			},
			// Legacy flat API
			map[string]interface{}{
				"required": []string{"schedule_type"},
			},
		},
	}
}

func (t *CreateScheduledTaskTool) Execute(ctx context.Context, args string) (string, error) {
	var in struct {
		Title         string `json:"title"`
		ActionType    string `json:"action_type"`
		ActionPayload string `json:"action_payload"`

		Schedule scheduleInput `json:"schedule"`

		// legacy flat fields
		ScheduleType string `json:"schedule_type"`
		RunAt        string `json:"run_at"`
		CronExpr     string `json:"cron_expr"`
		Timezone     string `json:"timezone"`
		Status       string `json:"status"`
	}
	if err := utils.UnmarshalLLMJSON(args, &in); err != nil {
		return "", fmt.Errorf("invalid JSON args: %w", err)
	}

	in.Title = strings.TrimSpace(in.Title)
	in.ActionType = strings.TrimSpace(in.ActionType)
	in.ActionPayload = strings.TrimSpace(in.ActionPayload)
	in.ScheduleType = strings.TrimSpace(in.ScheduleType)
	in.Status = strings.TrimSpace(in.Status)

	if in.Title == "" || in.ActionType == "" || in.ActionPayload == "" {
		return "", fmt.Errorf("title, action_type, action_payload are required")
	}
	if in.Status == "" {
		in.Status = "active"
	}
	// schedule_type will be derived from `schedule` when provided; otherwise fall back to legacy fields.
	if err := validateEnums(in.ActionType, "", in.Status); err != nil {
		return "", err
	}
	payload, err := normalizeActionPayload(in.ActionType, in.ActionPayload)
	if err != nil {
		return "", err
	}

	now := time.Now()

	// Adapt schedule
	var (
		scheduleType string
		runAt        time.Time
		cronExpr     string
		rruleText    string
		tz           string
		loc          *time.Location
	)
	if strings.TrimSpace(in.Schedule.Type) != "" {
		scheduleType, runAt, cronExpr, rruleText, tz, loc, err = adaptSchedule(now, in.Schedule, in.Timezone)
		if err != nil {
			return "", err
		}
	} else {
		// legacy flat fields
		loc, tz, err = loadLocation(in.Timezone)
		if err != nil {
			return "", err
		}
		scheduleType = strings.TrimSpace(in.ScheduleType)
		if scheduleType == "" {
			// default legacy
			scheduleType = "once"
		}
		if err := validateEnums("", scheduleType, ""); err != nil {
			return "", err
		}
		runAt, err = parseRunAtFlexible(in.RunAt, loc)
		if err != nil {
			return "", err
		}
		cronExpr = strings.TrimSpace(in.CronExpr)
	}

	var nextRun time.Time
	if strings.TrimSpace(rruleText) != "" {
		nextRun, err = computeNextRunFromRRule(now, rruleText, loc)
	} else {
		nextRun, err = computeNextRun(now, scheduleType, runAt, cronExpr, loc)
	}
	if err != nil {
		return "", err
	}

	if in.Status == "paused" {
		nextRun = time.Time{}
	}

	id := "task-" + utils.GenerateChatID()
	createdAt := now.UTC()
	updatedAt := createdAt

	db, err := openDB()
	if err != nil {
		return "", err
	}

	var runAtDB interface{}
	if !runAt.IsZero() {
		runAtDB = runAt.In(loc).UTC()
	} else {
		runAtDB = nil
	}
	var cronExprDB interface{}
	if strings.TrimSpace(cronExpr) != "" {
		cronExprDB = strings.TrimSpace(cronExpr)
	} else {
		cronExprDB = nil
	}
	var rruleDB interface{}
	if strings.TrimSpace(rruleText) != "" {
		rruleDB = strings.TrimSpace(rruleText)
	} else {
		rruleDB = nil
	}
	var nextRunDB interface{}
	if !nextRun.IsZero() {
		nextRunDB = nextRun.UTC()
	} else {
		nextRunDB = nil
	}

	_, err = db.Exec(
		`INSERT INTO scheduled_task
		(id, user_id, title, action_type, action_payload, schedule_type, run_at, cron_expr, rrule, timezone, status, last_run_at, next_run_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,NULL,?,?,?)`,
		id, "1", in.Title, in.ActionType, payload, scheduleType, runAtDB, cronExprDB, rruleDB, tz, in.Status, nextRunDB, createdAt, updatedAt,
	)
	if err != nil {
		return "", fmt.Errorf("insert scheduled_task: %w", err)
	}

	outTask := ScheduledTask{
		ID:            id,
		UserID:        "1",
		Title:         in.Title,
		ActionType:    in.ActionType,
		ActionPayload: payload,
		ScheduleType:  scheduleType,
		RunAt:         timeOrZero(runAtDB),
		CronExpr:      strings.TrimSpace(cronExpr),
		RRule:         strings.TrimSpace(rruleText),
		Timezone:      tz,
		Status:        in.Status,
		NextRunAt:     timeOrZero(nextRunDB),
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}

	return toJSON(map[string]interface{}{
		"success": true,
		"task":    outTask,
	})
}

func timeOrZero(v interface{}) time.Time {
	if v == nil {
		return time.Time{}
	}
	switch t := v.(type) {
	case time.Time:
		return t
	case sql.NullTime:
		if t.Valid {
			return t.Time
		}
	}
	return time.Time{}
}

func (t *CreateScheduledTaskTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Created scheduled task.",
		"properties": map[string]interface{}{
			"success": map[string]interface{}{"type": "boolean"},
			"task":    map[string]interface{}{"type": "object"},
		},
	}
}

func (t *CreateScheduledTaskTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicCrontab, "创建一次性或周期性（RRULE/cron）定时任务，并持久化到本地 SQLite。")
}
