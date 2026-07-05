package meitu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/credentials"
)

func TestClientSubmitSignsAndParsesTaskID(t *testing.T) {
	var sawAuth bool
	var sawDate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != SubmitPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, SubmitPath)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Fatalf("authorization = %q, want Bearer", auth)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			t.Fatalf("authorization is not base64: %v", err)
		}
		if !strings.Contains(string(decoded), "SDK-HMAC-SHA256 Access=ak") {
			t.Fatalf("decoded auth = %q", string(decoded))
		}
		sawAuth = true
		if r.Header.Get("X-Sdk-Date") == "" {
			t.Fatal("missing X-Sdk-Date")
		}
		sawDate = true

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["image_file"] != "https://example.com/input.png" {
			t.Fatalf("image_file = %#v", body["image_file"])
		}
		if body["sync_timeout"] != float64(1) {
			t.Fatalf("sync_timeout = %#v", body["sync_timeout"])
		}
		if body["subject_protect_flag"] != true {
			t.Fatalf("subject_protect_flag = %#v", body["subject_protect_flag"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"status":9,"result":{"id":"mt-task"}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	client.now = func() time.Time { return time.Unix(100, 0).UTC() }

	result, err := client.Submit(context.Background(), credentials.Credential{Name: "acc1", AppKey: "ak", SecretID: "sk"}, SubmitInput{
		ImageURL:           "https://example.com/input.png",
		SubjectProtectFlag: true,
	})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if result.UpstreamTaskID != "mt-task" {
		t.Fatalf("upstream task id = %s, want mt-task", result.UpstreamTaskID)
	}
	if !sawAuth || !sawDate {
		t.Fatal("server did not see auth/date headers")
	}
}

func TestClientStatusParsesProjectJSON(t *testing.T) {
	projectJSON := `{"templateConf":[{"width":2880,"height":1440,"layers":[{"id":"a"},{"id":"b"}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != StatusPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, StatusPath)
		}
		if r.URL.Query().Get("task_id") != "mt-task" {
			t.Fatalf("task_id = %s", r.URL.Query().Get("task_id"))
		}
		if r.Header.Get("X-Sdk-Content-Sha256") != "UNSIGNED-PAYLOAD" {
			t.Fatalf("content sha = %q", r.Header.Get("X-Sdk-Content-Sha256"))
		}

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
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	result, err := client.Status(context.Background(), credentials.Credential{Name: "acc1", AppKey: "ak", SecretID: "sk"}, "mt-task")
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if result.Status != UpstreamStatusCompleted {
		t.Fatalf("status = %d, want completed", result.Status)
	}
	if result.Width != 2880 || result.Height != 1440 || result.LayerCount != 2 {
		t.Fatalf("summary = %dx%d layers=%d", result.Width, result.Height, result.LayerCount)
	}
	if _, ok := result.ProjectJSON["templateConf"].([]any); !ok {
		t.Fatalf("project_json is not structured: %#v", result.ProjectJSON)
	}
}

func TestClientStatusInvalidProjectJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":       0,
			"error_code": 0,
			"data": map[string]any{
				"status": 10,
				"result": map[string]any{
					"parameters": map[string]any{
						"return_json_data": map[string]any{"json_data": `not-json`},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.Status(context.Background(), credentials.Credential{Name: "acc1", AppKey: "ak", SecretID: "sk"}, "mt-task")
	if err == nil {
		t.Fatal("Status succeeded, want parse error")
	}
	if !IsProjectJSONParseError(err) {
		t.Fatalf("error = %T %v, want ProjectJSONParseError", err, err)
	}
}

func TestClientStatusErrorUsesUpstreamMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":       0,
			"error_code": 30001,
			"error_msg":  "图片下载失败",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.Status(context.Background(), credentials.Credential{Name: "acc1", AppKey: "ak", SecretID: "sk"}, "mt-task")
	if err == nil {
		t.Fatal("Status succeeded, want upstream error")
	}
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %T %v, want UpstreamError", err, err)
	}
	if upstreamErr.Code != "error_code_30001" {
		t.Fatalf("code = %s, want error_code_30001", upstreamErr.Code)
	}
	if upstreamErr.Message != "图片下载失败" {
		t.Fatalf("message = %q, want 图片下载失败", upstreamErr.Message)
	}
}

func TestClientStatusStringErrorWithoutNumericCodeFails(t *testing.T) {
	tests := []struct {
		name        string
		body        map[string]any
		wantCode    string
		wantMessage string
	}{
		{
			name:        "error",
			body:        map[string]any{"code": 0, "error_code": 0, "error": "upstream_busy"},
			wantCode:    "upstream_busy",
			wantMessage: "upstream_busy",
		},
		{
			name:        "error_msg",
			body:        map[string]any{"code": 0, "error_code": 0, "error_msg": "图片下载失败"},
			wantCode:    "upstream_error",
			wantMessage: "图片下载失败",
		},
		{
			name:        "error_message",
			body:        map[string]any{"code": 0, "error_code": 0, "error_message": "图片格式不支持"},
			wantCode:    "upstream_error",
			wantMessage: "图片格式不支持",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(tt.body)
			}))
			defer server.Close()

			client := NewClient(server.URL, server.Client())
			_, err := client.Status(context.Background(), credentials.Credential{Name: "acc1", AppKey: "ak", SecretID: "sk"}, "mt-task")
			if err == nil {
				t.Fatal("Status succeeded, want upstream error")
			}
			var upstreamErr *UpstreamError
			if !errors.As(err, &upstreamErr) {
				t.Fatalf("error = %T %v, want UpstreamError", err, err)
			}
			if upstreamErr.Code != tt.wantCode {
				t.Fatalf("code = %s, want %s", upstreamErr.Code, tt.wantCode)
			}
			if upstreamErr.Message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", upstreamErr.Message, tt.wantMessage)
			}
		})
	}
}

func TestClientStatusMapsUnknownTerminalStatusToFailedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":       0,
			"error_code": 0,
			"data": map[string]any{
				"status":   11,
				"progress": 1,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	result, err := client.Status(context.Background(), credentials.Credential{Name: "acc1", AppKey: "ak", SecretID: "sk"}, "mt-task")
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if !result.Failed {
		t.Fatalf("Failed = false, want true")
	}
	if result.FailureCode != "status_11" {
		t.Fatalf("FailureCode = %s, want status_11", result.FailureCode)
	}
}
