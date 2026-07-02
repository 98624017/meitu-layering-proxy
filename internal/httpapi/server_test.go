package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/auth"
	"github.com/98624017/meitu-layering-proxy/internal/tasks"
)

func TestCreateTaskValidation(t *testing.T) {
	service := &fakeTaskService{}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"meitu-layering","image":"http://127.0.0.1/input.png"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), CodeInvalidRequest) {
		t.Fatalf("body = %s, want invalid_request", recorder.Body.String())
	}
	if service.created {
		t.Fatal("task was created despite invalid request")
	}
}

func TestCreateTaskRejectsDomainResolvingToPrivateIP(t *testing.T) {
	oldResolver := publicURLResolver
	publicURLResolver = fakeResolver{
		"127.0.0.1.nip.io": {{IP: net.ParseIP("127.0.0.1")}},
	}
	defer func() { publicURLResolver = oldResolver }()

	service := &fakeTaskService{}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"meitu-layering","image":"http://127.0.0.1.nip.io/input.png"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if service.created {
		t.Fatal("task was created despite private DNS result")
	}
}

func TestCreateTaskRejectsUnsupportedModel(t *testing.T) {
	oldResolver := publicURLResolver
	publicURLResolver = fakeResolver{"example.com": {{IP: net.ParseIP("93.184.216.34")}}}
	defer func() { publicURLResolver = oldResolver }()

	service := &fakeTaskService{}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"other-video-model","image":"https://example.com/input.png"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), CodeInvalidRequest) {
		t.Fatalf("body = %s, want invalid_request", recorder.Body.String())
	}
	if service.created {
		t.Fatal("task was created despite unsupported model")
	}
}

func TestCreateTaskReturnsQueuedVideoResponse(t *testing.T) {
	oldResolver := publicURLResolver
	publicURLResolver = fakeResolver{"example.com": {{IP: net.ParseIP("93.184.216.34")}}}
	defer func() { publicURLResolver = oldResolver }()

	now := time.Unix(100, 0).UTC()
	service := &fakeTaskService{
		task: &tasks.Task{
			ID:              "meitu_task_1",
			Model:           "meitu-layering",
			Status:          tasks.StatusQueued,
			Progress:        0,
			CreatedAt:       now,
			QueueDeadlineAt: now.Add(time.Minute),
		},
	}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"meitu-layering","prompt":"ignored","image":"https://example.com/input.png","subject_protect_flag":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response VideoResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "meitu_task_1" || response.TaskID != "meitu_task_1" {
		t.Fatalf("unexpected ids: %#v", response)
	}
	if response.Object != "video" || response.Status != "queued" {
		t.Fatalf("unexpected object/status: %#v", response)
	}
	if !service.created || service.subjectProtectFlag != true {
		t.Fatalf("service created=%v subjectProtectFlag=%v", service.created, service.subjectProtectFlag)
	}
}

func TestCreateTaskIgnoresUnsupportedVideoCompatibilityFields(t *testing.T) {
	oldResolver := publicURLResolver
	publicURLResolver = fakeResolver{"example.com": {{IP: net.ParseIP("93.184.216.34")}}}
	defer func() { publicURLResolver = oldResolver }()

	now := time.Unix(100, 0).UTC()
	service := &fakeTaskService{
		task: &tasks.Task{
			ID:        "meitu_task_1",
			Model:     "meitu-layering",
			Status:    tasks.StatusQueued,
			CreatedAt: now,
		},
	}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	body := `{
		"model":"meitu-layering",
		"image":"https://example.com/input.png",
		"duration":"5",
		"size":"1024x1024",
		"reference_images":["https://example.com/ignored.png"]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if !service.created {
		t.Fatal("task was not created")
	}
}

func TestCreateTaskQueueFull(t *testing.T) {
	oldResolver := publicURLResolver
	publicURLResolver = fakeResolver{"example.com": {{IP: net.ParseIP("93.184.216.34")}}}
	defer func() { publicURLResolver = oldResolver }()

	service := &fakeTaskService{createErr: tasks.ErrTaskQueueFull}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"meitu-layering","image":"https://example.com/input.png"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), CodeTaskQueueFull) {
		t.Fatalf("body = %s, want queue full code", recorder.Body.String())
	}
}

func TestGetTaskNotFound(t *testing.T) {
	server := NewServer(&fakeTaskService{}, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/videos/missing", nil)
	req.Header.Set("Authorization", "Bearer secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), CodeTaskNotFound) {
		t.Fatalf("body = %s, want task_not_found", recorder.Body.String())
	}
}

type fakeTaskService struct {
	task               *tasks.Task
	created            bool
	createErr          error
	subjectProtectFlag bool
}

func (f *fakeTaskService) CreateTask(model string, imageURL string, subjectProtectFlag bool) (*tasks.Task, error) {
	f.created = true
	f.subjectProtectFlag = subjectProtectFlag
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.task != nil {
		return f.task, nil
	}
	return &tasks.Task{
		ID:        "meitu_task_generated",
		Model:     model,
		Status:    tasks.StatusQueued,
		CreatedAt: time.Unix(100, 0),
	}, nil
}

type fakeResolver map[string][]net.IPAddr

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	addresses, ok := f[host]
	if !ok {
		return nil, errors.New("host not found")
	}
	return addresses, nil
}

func (f *fakeTaskService) RefreshTask(ctx context.Context, id string) (*tasks.Task, bool) {
	if f.task == nil || f.task.ID != id {
		return nil, false
	}
	return f.task, true
}
