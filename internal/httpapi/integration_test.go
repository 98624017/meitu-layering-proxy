package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/auth"
	"github.com/98624017/meitu-layering-proxy/internal/config"
	"github.com/98624017/meitu-layering-proxy/internal/credentials"
	"github.com/98624017/meitu-layering-proxy/internal/meitu"
	"github.com/98624017/meitu-layering-proxy/internal/tasks"
)

func TestProxyWithFakeMeituServer(t *testing.T) {
	oldResolver := publicURLResolver
	publicURLResolver = fakeResolver{"example.com": {{IP: net.ParseIP("93.184.216.34")}}}
	defer func() { publicURLResolver = oldResolver }()

	var submitCalls atomic.Int64
	var statusCalls atomic.Int64

	fakeMeitu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case meitu.SubmitPath:
			submitCalls.Add(1)
			if r.Header.Get("Authorization") == "" {
				t.Fatal("missing upstream Authorization")
			}
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode submit body: %v", err)
			}
			if request["image_file"] != "https://example.com/input.png" {
				t.Fatalf("image_file = %#v", request["image_file"])
			}
			if request["sync_timeout"] != float64(1) {
				t.Fatalf("sync_timeout = %#v", request["sync_timeout"])
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"status":9,"result":{"id":"mt-task"}}}`))
		case meitu.StatusPath:
			statusCalls.Add(1)
			if r.URL.Query().Get("task_id") != "mt-task" {
				t.Fatalf("task_id = %s", r.URL.Query().Get("task_id"))
			}
			projectJSON := `{"templateConf":[{"width":100,"height":200,"layers":[{"id":"layer1"}]}]}`
			response := map[string]any{
				"code":       0,
				"error_code": 0,
				"message":    "success",
				"data": map[string]any{
					"status":   10,
					"progress": 1,
					"result": map[string]any{
						"id": "mt-task",
						"parameters": map[string]any{
							"return_json_data": map[string]any{
								"code":      0,
								"json_data": projectJSON,
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fakeMeitu.Close()

	store := tasks.NewMemoryStore()
	pool := credentials.NewPool([]config.CredentialConfig{{
		Name:           "acc1",
		AppKey:         "ak",
		SecretID:       "sk",
		MaxConcurrency: 1,
	}})
	taskService := tasks.NewService(tasks.ServiceOptions{
		Store:                store,
		Pool:                 pool,
		Client:               meitu.NewClient(fakeMeitu.URL, fakeMeitu.Client()),
		QueueTimeout:         time.Minute,
		UpstreamLeaseTimeout: time.Minute,
		TaskTTL:              time.Hour,
		MaxQueuedTasks:       100,
	})
	server := NewServer(taskService, auth.NewMiddleware("proxy-secret"))
	handler := server.Handler()

	createReq := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"meitu-layering","prompt":"ignored","image":"https://example.com/input.png"}`))
	createReq.Header.Set("Authorization", "Bearer proxy-secret")
	createReq.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, createReq)

	if createRecorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	var createResponse VideoResponse
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &createResponse); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResponse.Status != "queued" {
		t.Fatalf("create status = %s, want queued", createResponse.Status)
	}
	if submitCalls.Load() != 0 || statusCalls.Load() != 0 {
		t.Fatalf("upstream calls immediately after POST submit=%d status=%d, want 0/0", submitCalls.Load(), statusCalls.Load())
	}

	taskService.ProcessOnce(context.Background())
	if submitCalls.Load() != 1 {
		t.Fatalf("submit calls = %d, want 1", submitCalls.Load())
	}
	if statusCalls.Load() != 0 {
		t.Fatalf("status calls = %d, want 0 before GET", statusCalls.Load())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/videos/"+createResponse.TaskID, nil)
	getReq.Header.Set("Authorization", "Bearer proxy-secret")
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getReq)

	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	if statusCalls.Load() != 1 {
		t.Fatalf("status calls = %d, want 1", statusCalls.Load())
	}

	var getResponse VideoResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &getResponse); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getResponse.Status != "completed" {
		t.Fatalf("get status = %s, want completed", getResponse.Status)
	}
	meituMetadata, ok := getResponse.Metadata["meitu"].(map[string]any)
	if !ok {
		t.Fatalf("missing meitu metadata: %#v", getResponse.Metadata)
	}
	if _, ok := meituMetadata["project_json"].(map[string]any); !ok {
		t.Fatalf("project_json is not object: %#v", meituMetadata["project_json"])
	}
	if !strings.Contains(getRecorder.Body.String(), `"layer_count":1`) {
		t.Fatalf("response missing layer count: %s", getRecorder.Body.String())
	}
}
