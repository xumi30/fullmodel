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
)

type Task struct {
	ID        string     `json:"id"`
	Status    TaskStatus `json:"status"`
	Error     string     `json:"error,omitempty"`
	Result    *Result    `json:"result,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type TaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*Task
	jobs  chan taskJob
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
		tasks: make(map[string]*Task),
		jobs:  make(chan taskJob, 128),
	}
	for i := 0; i < workerCount; i++ {
		go s.worker()
	}
	return s
}

func (s *TaskStore) Start(ctx context.Context, runner *Runner, request Request) (*Task, error) {
	if s == nil {
		return nil, fmt.Errorf("task store is nil")
	}
	if runner == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	id, err := randomID("task")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	task := &Task{
		ID:        id,
		Status:    TaskQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.tasks[id] = task
	s.mu.Unlock()

	s.jobs <- taskJob{ctx: ctx, runner: runner, request: request, taskID: id}
	return task.Clone(), nil
}

func (s *TaskStore) worker() {
	for job := range s.jobs {
		s.run(job)
	}
}

func (s *TaskStore) run(job taskJob) {
	s.update(job.taskID, func(t *Task) {
		t.Status = TaskRunning
	})
	result, err := job.runner.Run(job.ctx, job.request)
	s.update(job.taskID, func(t *Task) {
		if err != nil {
			t.Status = TaskFailed
			t.Error = err.Error()
			return
		}
		t.Status = TaskSucceeded
		t.Result = result
	})
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

func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}
