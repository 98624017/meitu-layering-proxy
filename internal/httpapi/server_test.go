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
	"github.com/98624017/meitu-layering-proxy/internal/meitu"
	"github.com/98624017/meitu-layering-proxy/internal/tasks"
)

func TestCreateTaskValidation(t *testing.T) {
	service := &fakeTaskService{}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"layering-v2","image":"http://127.0.0.1/input.png"}`))
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

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"layering-v2","image":"http://127.0.0.1.nip.io/input.png"}`))
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
	for _, tc := range []struct {
		model, message string
	}{
		{"meitu-layering", "meitu-layering 已停用，请升级客户端并使用 layering-v2"},
		{" meitu-layering ", "meitu-layering 已停用，请升级客户端并使用 layering-v2"},
		{"other-video-model", "model 不支持"},
		{"", "model 是必填字段"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			service := &fakeTaskService{}
			server := NewServer(service, auth.NewMiddleware("secret"))
			body, err := json.Marshal(map[string]string{"model": tc.model})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			var response ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusBadRequest || response.Error.Code != CodeInvalidRequest || response.Error.Message != tc.message || service.created {
				t.Fatalf("status=%d error=%+v created=%v", recorder.Code, response.Error, service.created)
			}
		})
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
			Model:           "layering-v2",
			Status:          tasks.StatusQueued,
			Progress:        0,
			CreatedAt:       now,
			QueueDeadlineAt: now.Add(time.Minute),
		},
	}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"layering-v2","prompt":"ignored","image":"https://example.com/input.png","subject_protect_flag":true}`))
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
	if !service.created || !service.options.SubjectProtectFlag || !service.options.TextEditable {
		t.Fatalf("service created=%v options=%+v", service.created, service.options)
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
			Model:     "layering-v2",
			Status:    tasks.StatusQueued,
			CreatedAt: now,
		},
	}
	server := NewServer(service, auth.NewMiddleware("secret"))
	handler := server.Handler()

	body := `{
		"model":"layering-v2",
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

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"layering-v2","image":"https://example.com/input.png"}`))
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

func TestLayeringOptions(t *testing.T) {
	oldResolver := publicURLResolver
	publicURLResolver = fakeResolver{"example.com": {{IP: net.ParseIP("93.184.216.34")}}}
	defer func() { publicURLResolver = oldResolver }()
	for _, tc := range []struct {
		name, fields string
		wantError    bool
		want         meitu.LayeringOptions
	}{
		{"defaults", ``, false, meitu.LayeringOptions{TextEditable: true, OriLang: "ch"}},
		{"non_editable", `,"text_editable":false`, false, meitu.LayeringOptions{OriLang: "ch"}},
		{"options", `,"text_editable":true,"subject_protect_flag":true,"ori_lang":"korean","only_text_eliminate":true`, false, meitu.LayeringOptions{TextEditable: true, SubjectProtectFlag: true, OriLang: "korean", OnlyTextEliminate: true}},
		{"string_boolean", `,"text_editable":"false"`, true, meitu.LayeringOptions{}},
		{"invalid_eliminate", `,"only_text_eliminate":"true"`, true, meitu.LayeringOptions{}},
		{"invalid_protect", `,"subject_protect_flag":1`, true, meitu.LayeringOptions{}},
		{"invalid_language", `,"ori_lang":"auto"`, true, meitu.LayeringOptions{}},
		{"prompt_ignored", `,"prompt":"{\"text_editable\":false}"`, false, meitu.LayeringOptions{TextEditable: true, OriLang: "ch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeTaskService{}
			server := NewServer(service, auth.NewMiddleware("secret"))
			body := `{"model":"layering-v2","input_reference":"https://example.com/input.png"` + tc.fields + `}`
			req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			wantStatus := http.StatusOK
			if tc.wantError {
				wantStatus = http.StatusBadRequest
			}
			if recorder.Code != wantStatus || service.created == tc.wantError {
				t.Fatalf("status=%d created=%v body=%s", recorder.Code, service.created, recorder.Body.String())
			}
			if !tc.wantError && service.options != tc.want {
				t.Fatalf("options=%+v want=%+v", service.options, tc.want)
			}
		})
	}
	_, _, _, err := ValidateCreateRequest(CreateVideoRequest{Model: "layering-v2", Image: "https://example.com/a.png", InputReference: "https://example.com/b.png"})
	if err == nil {
		t.Fatal("conflicting image aliases must fail")
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
	task      *tasks.Task
	created   bool
	createErr error
	options   meitu.LayeringOptions
}

func (f *fakeTaskService) CreateTask(model string, imageURL string, options meitu.LayeringOptions) (*tasks.Task, error) {
	f.created = true
	f.options = options
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
