package crontab

import (
	"database/sql"
	"fmt"
	"github.com/xumi30/fullmodel/utils/logging"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

const scheduledTasksTable = `
CREATE TABLE IF NOT EXISTS scheduled_task (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	title TEXT NOT NULL,
	action_type TEXT NOT NULL CHECK(action_type IN ('notify', 'tool')),
	action_payload TEXT NOT NULL,
	schedule_type TEXT NOT NULL CHECK(schedule_type IN ('once', 'recurring')),
	run_at DATETIME,
	cron_expr TEXT,
	rrule TEXT,
	timezone TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'paused', 'deleted')),
	last_run_at DATETIME,
	next_run_at DATETIME,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scheduled_task_user_id ON scheduled_task(user_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_task_status ON scheduled_task(status);
CREATE INDEX IF NOT EXISTS idx_scheduled_task_next_run_at ON scheduled_task(next_run_at);
`

var (
	taskDB     *sql.DB
	taskDBOnce sync.Once
	taskDBErr  error
)

func dbPath() string {
	abs, err := filepath.Abs(filepath.Join(".", "data", "scheduled_tasks.db"))
	if err != nil {
		return filepath.Join(".", "data", "scheduled_tasks.db")
	}
	return abs
}

func openDB() (*sql.DB, error) {
	taskDBOnce.Do(func() {
		p := dbPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			taskDBErr = err
			return
		}
		dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", filepath.ToSlash(p))
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			taskDBErr = err
			return
		}
		if _, err := db.Exec(scheduledTasksTable); err != nil {
			_ = db.Close()
			taskDBErr = err
			return
		}
		// Best-effort schema migration for older DBs (thread runner requires these columns too).
		if err := ensureScheduledTaskSchema(db); err != nil {
			_ = db.Close()
			taskDBErr = err
			return
		}
		taskDB = db
	})
	if taskDBErr != nil {
		return nil, taskDBErr
	}
	return taskDB, nil
}

func ensureScheduledTaskSchema(db *sql.DB) error {
	cols, err := listColumns(db, "scheduled_task")
	if err != nil {
		return err
	}
	type colDef struct {
		name string
		ddl  string
	}
	defs := []colDef{
		{name: "rrule", ddl: `ALTER TABLE scheduled_task ADD COLUMN rrule TEXT`},
		{name: "executing", ddl: `ALTER TABLE scheduled_task ADD COLUMN executing INTEGER NOT NULL DEFAULT 0`},
		{name: "version", ddl: `ALTER TABLE scheduled_task ADD COLUMN version INTEGER NOT NULL DEFAULT 0`},
		{name: "completed", ddl: `ALTER TABLE scheduled_task ADD COLUMN completed INTEGER NOT NULL DEFAULT 0`},
		{name: "run_count", ddl: `ALTER TABLE scheduled_task ADD COLUMN run_count INTEGER NOT NULL DEFAULT 0`},
	}
	for _, d := range defs {
		if cols[d.name] {
			continue
		}
		if _, err := db.Exec(d.ddl); err != nil {
			logging.Error("scheduled_task migrate failed col=%s err=%v", d.name, err)
			return fmt.Errorf("migrate scheduled_task add column %s: %w", d.name, err)
		}
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduled_task_due ON scheduled_task(status, completed, executing, next_run_at)`)
	return nil
}

func listColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    interface{}
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan pragma table_info(%s): %w", table, err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}
