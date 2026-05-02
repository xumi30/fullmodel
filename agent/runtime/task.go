package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
)

type Task struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	Status     TaskStatus     `json:"status"`
	Progress   float64        `json:"progress"`
	Error      string         `json:"error,omitempty"`
	Result     *Result        `json:"result,omitempty"`
	Artifacts  []Artifact     `json:"artifacts,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type TaskOptions struct {
	Kind      string
	SessionID string
	Metadata  map[string]any
}

type TaskStore struct {
	mu      sync.RWMutex
	tasks   map[string]*Task
	cancels map[string]context.CancelFunc
	jobs    chan taskJob
}

type taskJob struct {
	ctx     context.Context
	runner  *Runner
	request Request
	taskID  string
}

func NewTaskStore(workers ...int) *TaskStore {
	workerCount := 2
	if len(workers) > 0 && workers[0] > 0 {
		workerCount = workers[0]
	}
	s := &TaskStore{
		tasks:   make(map[string]*Task),
		cancels: make(map[string]context.CancelFunc),
		jobs:    make(chan taskJob, 128),
	}
	for i := 0; i < workerCount; i++ {
		go s.worker()
	}
	return s
}

func (s *TaskStore) Start(ctx context.Context, runner *Runner, request Request) (*Task, error) {
	return s.StartWithOptions(ctx, runner, request, TaskOptions{})
}

func (s *TaskStore) StartWithOptions(ctx context.Context, runner *Runner, request Request, opts TaskOptions) (*Task, error) {
	if s == nil {
		return nil, fmt.Errorf("task store is nil")
	}
	if runner == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	taskCtx, cancel := context.WithCancel(ctx)
	id, err := randomID("task")
	if err != nil {
		cancel()
		return nil, err
	}
	now := time.Now().UTC()
	task := &Task{
		ID:        id,
		Kind:      opts.Kind,
		SessionID: opts.SessionID,
		Status:    TaskQueued,
		Metadata:  cloneMetadata(opts.Metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.tasks[id] = task
	s.cancels[id] = cancel
	s.mu.Unlock()

	s.jobs <- taskJob{ctx: taskCtx, runner: runner, request: request, taskID: id}
	return task.Clone(), nil
}

func (s *TaskStore) worker() {
	for job := range s.jobs {
		s.run(job)
	}
}

func (s *TaskStore) run(job taskJob) {
	if job.ctx.Err() != nil {
		s.finishCanceled(job.taskID)
		return
	}
	startedAt := time.Now().UTC()
	s.update(job.taskID, func(t *Task) {
		if t.Status == TaskCanceled {
			return
		}
		t.Status = TaskRunning
		t.Progress = 0.1
		t.StartedAt = &startedAt
	})
	if status := s.status(job.taskID); status == TaskCanceled {
		s.finishCanceled(job.taskID)
		return
	}
	result, err := job.runner.Run(job.ctx, job.request)
	if job.ctx.Err() != nil {
		s.finishCanceled(job.taskID)
		return
	}
	s.update(job.taskID, func(t *Task) {
		if err != nil {
			t.Status = TaskFailed
			t.Error = err.Error()
			t.Progress = 1
			finishedAt := time.Now().UTC()
			t.FinishedAt = &finishedAt
			return
		}
		t.Status = TaskSucceeded
		t.Result = result
		t.Progress = 1
		finishedAt := time.Now().UTC()
		t.FinishedAt = &finishedAt
	})
	s.forgetCancel(job.taskID)
}

func (s *TaskStore) Get(id string) (*Task, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	return task.Clone(), true
}

func (s *TaskStore) List() []*Task {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		out = append(out, task.Clone())
	}
	return out
}

func (s *TaskStore) Cancel(id string) (*Task, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	task, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return nil, false
	}
	cancel := s.cancels[id]
	if task.Status == TaskQueued || task.Status == TaskRunning {
		task.Status = TaskCanceled
		task.Progress = 1
		task.Error = "canceled"
		now := time.Now().UTC()
		task.FinishedAt = &now
		task.UpdatedAt = now
		delete(s.cancels, id)
	}
	cp := task.Clone()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return cp, true
}

func (s *TaskStore) SetArtifacts(id string, artifacts []Artifact) (*Task, bool) {
	if s == nil {
		return nil, false
	}
	var out *Task
	s.update(id, func(t *Task) {
		t.Artifacts = append([]Artifact(nil), artifacts...)
		out = t.Clone()
	})
	return out, out != nil
}

func (s *TaskStore) update(id string, fn func(*Task)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[id]
	if task == nil {
		return
	}
	fn(task)
	task.UpdatedAt = time.Now().UTC()
}

func (s *TaskStore) status(id string) TaskStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task := s.tasks[id]
	if task == nil {
		return ""
	}
	return task.Status
}

func (s *TaskStore) finishCanceled(id string) {
	s.update(id, func(t *Task) {
		t.Status = TaskCanceled
		t.Progress = 1
		t.Error = "canceled"
		finishedAt := time.Now().UTC()
		t.FinishedAt = &finishedAt
	})
	s.forgetCancel(id)
}

func (s *TaskStore) forgetCancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
}

func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	cp.Artifacts = append([]Artifact(nil), t.Artifacts...)
	cp.Metadata = cloneMetadata(t.Metadata)
	return &cp
}

func cloneMetadata(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
