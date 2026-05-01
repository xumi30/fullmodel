package crontab

import "time"

// ScheduledTask matches the storage schema for scheduled tasks.
type ScheduledTask struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Title         string    `json:"title"`
	ActionType    string    `json:"action_type"`   // notify / tool
	ActionPayload string    `json:"action_payload"`
	ScheduleType  string    `json:"schedule_type"` // once / recurring
	RunAt         time.Time `json:"run_at"`        // once
	CronExpr      string    `json:"cron_expr"`     // recurring
	RRule         string    `json:"rrule"`         // recurring (preferred)
	Timezone      string    `json:"timezone"`
	Status        string    `json:"status"` // active / paused / deleted
	LastRunAt     time.Time `json:"last_run_at"`
	NextRunAt     time.Time `json:"next_run_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

