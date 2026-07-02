package tasks

import (
	"testing"
	"time"
)

func TestMemoryStoreUpdateAndGet(t *testing.T) {
	store := NewMemoryStore()
	now := time.Unix(100, 0)
	task := &Task{ID: "task1", Status: StatusQueued, CreatedAt: now}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	if err := store.Update("task1", func(current *Task) error {
		current.Status = StatusCompleted
		current.CompletedAt = now
		return nil
	}); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	got, ok := store.Get("task1")
	if !ok {
		t.Fatal("task not found")
	}
	if got.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", got.Status)
	}
}

func TestMemoryStoreDeleteExpiredTerminalTasks(t *testing.T) {
	store := NewMemoryStore()
	now := time.Unix(100, 0)
	if err := store.Create(&Task{ID: "task1", Status: StatusCompleted, CompletedAt: now}); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := store.Create(&Task{ID: "task2", Status: StatusQueued}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	deleted := store.DeleteExpired(now.Add(2*time.Hour), time.Hour)
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, ok := store.Get("task1"); ok {
		t.Fatal("expired terminal task still exists")
	}
	if _, ok := store.Get("task2"); !ok {
		t.Fatal("non-terminal task was deleted")
	}
}
