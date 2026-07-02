package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/config"
	"github.com/98624017/meitu-layering-proxy/internal/credentials"
	"github.com/98624017/meitu-layering-proxy/internal/meitu"
)

func TestServiceQueueTimeoutBeforeUpstream(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := NewMemoryStore()
	pool := credentials.NewPool(nil)
	client := &fakeMeituClient{}
	service := NewService(ServiceOptions{
		Store:                store,
		Pool:                 pool,
		Client:               client,
		QueueTimeout:         time.Second,
		UpstreamLeaseTimeout: time.Minute,
		TaskTTL:              time.Hour,
		MaxQueuedTasks:       100,
	})
	SetNowForTest(service, func() time.Time { return now })

	task, err := service.CreateTask("meitu-layering", "https://example.com/image.png", false)
	if err != nil {
		t.Fatalf("CreateTask error: %v", err)
	}

	now = now.Add(2 * time.Second)
	service.ProcessOnce(context.Background())

	got, ok := service.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if got.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if got.Error == nil || got.Error.Code != ErrorQueueTimeoutBeforeUpstream {
		t.Fatalf("error = %#v, want queue timeout", got.Error)
	}
	if got.RequestedUpstream {
		t.Fatal("RequestedUpstream = true, want false")
	}
}

func TestServiceUpstreamLeaseTimeoutReleasesCredential(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := NewMemoryStore()
	pool := credentials.NewPool([]config.CredentialConfig{{
		Name:           "acc1",
		AppKey:         "ak",
		SecretID:       "sk",
		MaxConcurrency: 1,
	}})
	client := &fakeMeituClient{
		submitResult: meitu.SubmitResult{UpstreamTaskID: "mt-task", Status: 9},
	}
	service := NewService(ServiceOptions{
		Store:                store,
		Pool:                 pool,
		Client:               client,
		QueueTimeout:         time.Minute,
		UpstreamLeaseTimeout: time.Second,
		TaskTTL:              time.Hour,
		MaxQueuedTasks:       100,
	})
	SetNowForTest(service, func() time.Time { return now })

	task, err := service.CreateTask("meitu-layering", "https://example.com/image.png", false)
	if err != nil {
		t.Fatalf("CreateTask error: %v", err)
	}
	service.ProcessOnce(context.Background())

	if pool.ActiveCount("acc1") != 1 {
		t.Fatalf("active count after submit = %d, want 1", pool.ActiveCount("acc1"))
	}

	now = now.Add(2 * time.Second)
	refreshed, ok := service.RefreshTask(context.Background(), task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if refreshed.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", refreshed.Status)
	}
	if refreshed.Error == nil || refreshed.Error.Code != ErrorUpstreamLeaseTimeout {
		t.Fatalf("error = %#v, want lease timeout", refreshed.Error)
	}
	if pool.ActiveCount("acc1") != 0 {
		t.Fatalf("active count after timeout = %d, want 0", pool.ActiveCount("acc1"))
	}
	if client.statusCalls != 0 {
		t.Fatalf("status calls = %d, want 0 because lease timeout does not poll", client.statusCalls)
	}
}

func TestServiceCompletesOnRealtimeGet(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := NewMemoryStore()
	pool := credentials.NewPool([]config.CredentialConfig{{
		Name:           "acc1",
		AppKey:         "ak",
		SecretID:       "sk",
		MaxConcurrency: 1,
	}})
	client := &fakeMeituClient{
		submitResult: meitu.SubmitResult{UpstreamTaskID: "mt-task", Status: 9},
		statusResult: meitu.StatusResult{
			Status:   meitu.UpstreamStatusCompleted,
			Progress: 1,
			ProjectJSON: map[string]any{"templateConf": []any{map[string]any{
				"width":  float64(10),
				"height": float64(20),
				"layers": []any{map[string]any{"id": "layer1"}},
			}}},
			Width:      10,
			Height:     20,
			LayerCount: 1,
		},
	}
	service := NewService(ServiceOptions{
		Store:                store,
		Pool:                 pool,
		Client:               client,
		QueueTimeout:         time.Minute,
		UpstreamLeaseTimeout: time.Minute,
		TaskTTL:              time.Hour,
		MaxQueuedTasks:       100,
	})
	SetNowForTest(service, func() time.Time { return now })

	task, err := service.CreateTask("meitu-layering", "https://example.com/image.png", true)
	if err != nil {
		t.Fatalf("CreateTask error: %v", err)
	}
	service.ProcessOnce(context.Background())

	submitted, ok := service.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if submitted.Status != StatusInProgress {
		t.Fatalf("status after scheduler = %s, want in_progress", submitted.Status)
	}
	if client.statusCalls != 0 {
		t.Fatalf("status calls after scheduler = %d, want 0", client.statusCalls)
	}

	now = now.Add(time.Second)
	completed, ok := service.RefreshTask(context.Background(), task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if completed.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", completed.Status)
	}
	if completed.LayerCount != 1 {
		t.Fatalf("layer count = %d, want 1", completed.LayerCount)
	}
	if pool.ActiveCount("acc1") != 0 {
		t.Fatalf("active count = %d, want 0", pool.ActiveCount("acc1"))
	}
	if client.statusCalls != 1 {
		t.Fatalf("status calls = %d, want 1", client.statusCalls)
	}
}

func TestServiceRetriesCredentialErrors(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := NewMemoryStore()
	pool := credentials.NewPool([]config.CredentialConfig{
		{Name: "acc1", AppKey: "ak1", SecretID: "sk1", MaxConcurrency: 1},
		{Name: "acc2", AppKey: "ak2", SecretID: "sk2", MaxConcurrency: 1},
	})
	client := &fakeMeituClient{
		submitByCredential: map[string]submitCallResult{
			"acc1": {err: meitu.NewUpstreamError(429, "rate_limit", "rate limited", nil)},
			"acc2": {result: meitu.SubmitResult{UpstreamTaskID: "mt-task-2", Status: 9}},
		},
	}
	service := NewService(ServiceOptions{
		Store:                store,
		Pool:                 pool,
		Client:               client,
		QueueTimeout:         time.Minute,
		UpstreamLeaseTimeout: time.Minute,
		TaskTTL:              time.Hour,
		MaxQueuedTasks:       100,
	})
	SetNowForTest(service, func() time.Time { return now })

	task, err := service.CreateTask("meitu-layering", "https://example.com/image.png", false)
	if err != nil {
		t.Fatalf("CreateTask error: %v", err)
	}
	service.ProcessOnce(context.Background())

	got, ok := service.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if got.Status != StatusInProgress {
		t.Fatalf("status = %s, want in_progress", got.Status)
	}
	if got.LeasedCredentialName != "acc2" {
		t.Fatalf("leased credential = %s, want acc2", got.LeasedCredentialName)
	}
	if got.UpstreamTaskID != "mt-task-2" {
		t.Fatalf("upstream id = %s, want mt-task-2", got.UpstreamTaskID)
	}
}

func TestServiceRejectsWhenTaskQueueIsFull(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService(ServiceOptions{
		Store:                NewMemoryStore(),
		Pool:                 credentials.NewPool(nil),
		Client:               &fakeMeituClient{},
		QueueTimeout:         time.Minute,
		UpstreamLeaseTimeout: time.Minute,
		TaskTTL:              time.Hour,
		MaxQueuedTasks:       1,
	})
	SetNowForTest(service, func() time.Time { return now })

	if _, err := service.CreateTask("meitu-layering", "https://example.com/1.png", false); err != nil {
		t.Fatalf("first CreateTask error: %v", err)
	}
	if _, err := service.CreateTask("meitu-layering", "https://example.com/2.png", false); !errors.Is(err, ErrTaskQueueFull) {
		t.Fatalf("second CreateTask error = %v, want ErrTaskQueueFull", err)
	}
}

type fakeMeituClient struct {
	submitResult       meitu.SubmitResult
	submitErr          error
	statusResult       meitu.StatusResult
	statusErr          error
	statusCalls        int
	submitByCredential map[string]submitCallResult
}

type submitCallResult struct {
	result meitu.SubmitResult
	err    error
}

func (f *fakeMeituClient) Submit(ctx context.Context, credential credentials.Credential, input meitu.SubmitInput) (meitu.SubmitResult, error) {
	if f.submitByCredential != nil {
		result, ok := f.submitByCredential[credential.Name]
		if ok {
			return result.result, result.err
		}
		return meitu.SubmitResult{}, errors.New("unexpected credential")
	}
	if f.submitErr != nil {
		return meitu.SubmitResult{}, f.submitErr
	}
	return f.submitResult, nil
}

func (f *fakeMeituClient) Status(ctx context.Context, credential credentials.Credential, upstreamTaskID string) (meitu.StatusResult, error) {
	f.statusCalls++
	if f.statusErr != nil {
		return meitu.StatusResult{}, f.statusErr
	}
	return f.statusResult, nil
}
