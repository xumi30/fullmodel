package crontab

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/teambition/rrule-go"
)

type toolActionPayload struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type scheduleInput struct {
	Type     string `json:"type"`      // once / cron / hourly (extendable)
	RunAt    string `json:"run_at"`    // once
	Timezone string `json:"timezone"`  // optional
	CronExpr string `json:"cron_expr"` // cron
	RRule    string `json:"rrule"`     // rrule
	Interval int    `json:"interval"`  // hourly interval
}

func loadLocation(tz string) (*time.Location, string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" || strings.EqualFold(tz, "local") {
		return time.Local, "Local", nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, "", fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, tz, nil
}

func validateEnums(actionType, scheduleType, status string) error {
	if actionType != "" && actionType != "notify" && actionType != "tool" {
		return fmt.Errorf("action_type must be notify or tool, got %q", actionType)
	}
	if scheduleType != "" && scheduleType != "once" && scheduleType != "recurring" {
		return fmt.Errorf("schedule_type must be once or recurring, got %q", scheduleType)
	}
	if status != "" && status != "active" && status != "paused" && status != "deleted" {
		return fmt.Errorf("status must be active, paused, or deleted, got %q", status)
	}
	return nil
}

func computeNextRun(now time.Time, scheduleType string, runAt time.Time, cronExpr string, loc *time.Location) (time.Time, error) {
	switch scheduleType {
	case "once":
		if runAt.IsZero() {
			return time.Time{}, fmt.Errorf("run_at is required for schedule_type=once")
		}
		runLocal := runAt.In(loc)
		if !runLocal.After(now.In(loc)) {
			return time.Time{}, fmt.Errorf("run_at must be in the future")
		}
		return runLocal.UTC(), nil
	case "recurring":
		cronExpr = strings.TrimSpace(cronExpr)
		if cronExpr == "" {
			return time.Time{}, fmt.Errorf("cron_expr is required for schedule_type=recurring")
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(cronExpr)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid cron_expr: %w", err)
		}
		next := sched.Next(now.In(loc))
		if next.IsZero() {
			return time.Time{}, fmt.Errorf("cron_expr did not yield a next run time")
		}
		return next.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unknown schedule_type %q", scheduleType)
	}
}

func computeNextRunFromRRule(now time.Time, rruleText string, loc *time.Location) (time.Time, error) {
	rruleText = strings.TrimSpace(rruleText)
	if rruleText == "" {
		return time.Time{}, fmt.Errorf("rrule is required for rrule schedule")
	}
	opt, err := rrule.StrToROption(rruleText)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid rrule: %w", err)
	}
	// rrule-go uses Dtstart for expansion baseline.
	// Use "now" in schedule timezone as DTSTART so rule is anchored reasonably.
	opt.Dtstart = now.In(loc)
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid rrule options: %w", err)
	}
	next := r.After(now.In(loc), false)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("rrule did not yield a next run time")
	}
	return next.UTC(), nil
}

func parseRunAtFlexible(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, loc); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation(time.RFC3339, raw, loc); err == nil {
		return parsed, nil
	}
	return time.Time{}, fmt.Errorf("invalid run_at %q: must be RFC3339 or 'YYYY-MM-DD HH:MM:SS'", raw)
}

// adaptSchedule maps the external schedule object into internal fields.
// Internal schedule_type stays {once, recurring} to remain compatible with the current table CHECK constraints.
func adaptSchedule(now time.Time, schedule scheduleInput, fallbackTZ string) (scheduleType string, runAt time.Time, cronExpr string, rruleText string, tz string, loc *time.Location, err error) {
	st := strings.TrimSpace(schedule.Type)
	tz = strings.TrimSpace(schedule.Timezone)
	if tz == "" {
		tz = strings.TrimSpace(fallbackTZ)
	}
	loc, tz, err = loadLocation(tz)
	if err != nil {
		return "", time.Time{}, "", "", "", nil, err
	}

	switch st {
	case "", "once":
		scheduleType = "once"
		runAt, err = parseRunAtFlexible(schedule.RunAt, loc)
		if err != nil {
			return "", time.Time{}, "", "", "", nil, err
		}
		return scheduleType, runAt, "", "", tz, loc, nil
	case "cron", "recurring":
		scheduleType = "recurring"
		cronExpr = strings.TrimSpace(schedule.CronExpr)
		return scheduleType, time.Time{}, cronExpr, "", tz, loc, nil
	case "hourly":
		interval := schedule.Interval
		if interval <= 0 {
			interval = 1
		}
		scheduleType = "recurring"
		// Prefer RRULE for LLM-friendliness; still compatible with internal recurring.
		rruleText = fmt.Sprintf("FREQ=HOURLY;INTERVAL=%d", interval)
		return scheduleType, time.Time{}, "", rruleText, tz, loc, nil
	case "rrule":
		scheduleType = "recurring"
		rruleText = strings.TrimSpace(schedule.RRule)
		return scheduleType, time.Time{}, "", rruleText, tz, loc, nil
	default:
		return "", time.Time{}, "", "", "", nil, fmt.Errorf("unsupported schedule.type %q", st)
	}
}

func normalizeActionPayload(actionType, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("action_payload is required")
	}
	switch actionType {
	case "notify":
		return raw, nil
	case "tool":
		var payload toolActionPayload
		candidate := prepareJSONBestEffort(raw)
		if err := json.Unmarshal([]byte(candidate), &payload); err != nil {
			return "", fmt.Errorf("action_payload must be JSON for action_type=tool: %w", err)
		}
		payload.Tool = strings.TrimSpace(payload.Tool)
		if payload.Tool == "" {
			return "", fmt.Errorf(`action_payload for tool must include non-empty field "tool"`)
		}
		if payload.Args == nil {
			payload.Args = map[string]interface{}{}
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("normalize action_payload: %w", err)
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("unknown action_type %q", actionType)
	}
}

func prepareJSONBestEffort(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func taskFromRow(
	id string,
	userID string,
	title string,
	actionType string,
	actionPayload string,
	scheduleType string,
	runAt sql.NullTime,
	cronExpr sql.NullString,
	rruleText sql.NullString,
	timezone string,
	status string,
	lastRunAt sql.NullTime,
	nextRunAt sql.NullTime,
	createdAt time.Time,
	updatedAt time.Time,
) ScheduledTask {
	var t ScheduledTask
	t.ID = id
	t.UserID = userID
	t.Title = title
	t.ActionType = actionType
	t.ActionPayload = actionPayload
	t.ScheduleType = scheduleType
	if runAt.Valid {
		t.RunAt = runAt.Time
	}
	if cronExpr.Valid {
		t.CronExpr = cronExpr.String
	}
	if rruleText.Valid {
		t.RRule = rruleText.String
	}
	t.Timezone = timezone
	t.Status = status
	if lastRunAt.Valid {
		t.LastRunAt = lastRunAt.Time
	}
	if nextRunAt.Valid {
		t.NextRunAt = nextRunAt.Time
	}
	t.CreatedAt = createdAt
	t.UpdatedAt = updatedAt
	return t
}

func toJSON(v interface{}) (string, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

