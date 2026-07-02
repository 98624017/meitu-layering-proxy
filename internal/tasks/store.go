package tasks

import (
	"errors"
	"sync"
	"time"
)

var ErrTaskNotFound = errors.New("task not found")
var ErrTaskQueueFull = errors.New("task queue full")

type TaskStore interface {
	Create(task *Task) error
	CreateIfBelowActiveLimit(task *Task, maxActive int) error
	Get(id string) (*Task, bool)
	Update(id string, fn func(*Task) error) error
	ListRunnable(now time.Time) []*Task
	ListUpstreamLeaseExpired(now time.Time) []*Task
	DeleteExpired(now time.Time, ttl time.Duration) int
}

type MemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*Task
	order []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks: make(map[string]*Task),
		order: make([]string, 0),
	}
}

func (s *MemoryStore) Create(task *Task) error {
	if task == nil {
		return errors.New("task is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[task.ID]; ok {
		return errors.New("task already exists")
	}

	copied := cloneTask(task)
	s.tasks[task.ID] = copied
	s.order = append(s.order, task.ID)
	return nil
}

func (s *MemoryStore) CreateIfBelowActiveLimit(task *Task, maxActive int) error {
	if task == nil {
		return errors.New("task is nil")
	}
	if maxActive <= 0 {
		return ErrTaskQueueFull
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[task.ID]; ok {
		return errors.New("task already exists")
	}
	if s.activeTaskCountLocked() >= maxActive {
		return ErrTaskQueueFull
	}

	copied := cloneTask(task)
	s.tasks[task.ID] = copied
	s.order = append(s.order, task.ID)
	return nil
}

func (s *MemoryStore) Get(id string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	return cloneTask(task), true
}

func (s *MemoryStore) Update(id string, fn func(*Task) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	return fn(task)
}

func (s *MemoryStore) ListRunnable(now time.Time) []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Task, 0)
	for _, id := range s.order {
		task, ok := s.tasks[id]
		if !ok {
			continue
		}
		if task.Status == StatusQueued {
			result = append(result, cloneTask(task))
		}
	}
	return result
}

func (s *MemoryStore) ListUpstreamLeaseExpired(now time.Time) []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Task, 0)
	for _, id := range s.order {
		task, ok := s.tasks[id]
		if !ok {
			continue
		}
		if task.Status == StatusInProgress &&
			!task.UpstreamLeaseDeadlineAt.IsZero() &&
			!now.Before(task.UpstreamLeaseDeadlineAt) {
			result = append(result, cloneTask(task))
		}
	}
	return result
}

func (s *MemoryStore) DeleteExpired(now time.Time, ttl time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	keptOrder := s.order[:0]
	deleted := 0
	for _, id := range s.order {
		task, ok := s.tasks[id]
		if !ok {
			continue
		}
		if task.IsTerminal() && !task.CompletedAt.IsZero() && now.Sub(task.CompletedAt) > ttl {
			delete(s.tasks, id)
			deleted++
			continue
		}
		keptOrder = append(keptOrder, id)
	}
	s.order = keptOrder
	return deleted
}

func (s *MemoryStore) activeTaskCountLocked() int {
	count := 0
	for _, task := range s.tasks {
		if !task.IsTerminal() {
			count++
		}
	}
	return count
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}
	copied := *task
	if task.Error != nil {
		taskError := *task.Error
		copied.Error = &taskError
	}
	if task.ProjectJSON != nil {
		copied.ProjectJSON = cloneMap(task.ProjectJSON)
	}
	return &copied
}

func cloneMap(value map[string]any) map[string]any {
	copied := make(map[string]any, len(value))
	for key, item := range value {
		copied[key] = item
	}
	return copied
}
