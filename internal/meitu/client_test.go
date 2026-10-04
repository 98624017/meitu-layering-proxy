package meitu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/credentials"
)

func TestClientSubmitSignsAndParsesTaskID(t *testing.T) {
	for _, editable := range []bool{true, false} {
		t.Run(map[bool]string{true: "editable", false: "non_editable"}[editable], func(t *testing.T) {
			var sawAuth bool
			var sawDate bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/sdk/sync/push" {
					t.Fatalf("unexpected v2 path = %s", r.URL.Path)
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
				if body["task"] != "/v1/poster_trans_rob/491768" || body["task_type"] != "formula" {
					t.Fatalf("unexpected v2 task: %#v", body)
				}
				images := body["init_images"].([]any)
				image := images[0].(map[string]any)
				profile := image["profile"].(map[string]any)
				if len(images) != 1 || image["url"] != "https://example.com/input.png" || profile["version"] != "v1" || profile["media_profiles"].(map[string]any)["media_data_type"] != "url" {
					t.Fatalf("invalid input media: %#v", images)
				}
				if body["sync_timeout"] != float64(1) {
					t.Fatalf("sync_timeout = %#v", body["sync_timeout"])
				}
				var params struct {
					Parameter    map[string]any `json:"parameter"`
					RspMediaType string         `json:"rsp_media_type"`
				}
				if err := json.Unmarshal([]byte(body["params"].(string)), &params); err != nil {
					t.Fatalf("params must be a JSON string: %v", err)
				}
				wantSide, wantPSD := "", "1"
				if !editable {
					wantSide, wantPSD = "text_none_editable", "0"
				}
				want := map[string]any{"eliminate_type": "big", "only_text_eliminate": true, "ori_lang": "en", "poster_translate_flag": "9", "subject_protect_flag": true, "target_lang": "ch", "business_side_flag": wantSide, "generate_picture_flag": "0", "convert_json_psd_flag": wantPSD}
				if params.RspMediaType != "url" || !reflect.DeepEqual(params.Parameter, want) {
					t.Fatalf("unexpected v2 params: %+v", params)
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0,"error_code":0,"data":{"status":9,"task_id":"mt-task","result":{"id":"result-alias"}}}`))
			}))
			defer server.Close()

			client := NewClient(server.URL, server.Client())
			client.now = func() time.Time { return time.Unix(100, 0).UTC() }

			result, err := client.Submit(context.Background(), credentials.Credential{Name: "acc1", AppKey: "ak", SecretID: "sk"}, SubmitInput{
				ImageURL: "https://example.com/input.png",
				Options:  LayeringOptions{TextEditable: editable, SubjectProtectFlag: true, OriLang: "en", OnlyTextEliminate: true},
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
		})
	}
}

func TestClientStatusParsesProjectJSON(t *testing.T) {
	projectJSON := `{"width":2880,"height":1440,"templateConf":[{"id":"a","width":100,"height":200},{"id":"b"}],"image_psd_url":"https://example.com/result.psd"}`
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
	if result.PSDURL != "https://example.com/result.psd" {
		t.Fatalf("PSDURL = %q", result.PSDURL)
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

func TestClientV2SubmitErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"error_code":20001,"message":"PROCESS_ERROR","data":{"status":2}}`))
	}))
	defer server.Close()
	_, err := NewClient(server.URL, server.Client()).Submit(context.Background(), credentials.Credential{AppKey: "ak", SecretID: "sk"}, SubmitInput{})
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) || upstreamErr.Code != "error_code_20001" || upstreamErr.Message != "PROCESS_ERROR" {
		t.Fatalf("unexpected submit error: %v", err)
	}
}

func TestV2ResultRequiresPSDAndChecksAlgorithmErrors(t *testing.T) {
	for _, tc := range []struct{ name, result, wantCode string }{
		{"missing_psd", `{"parameters":{"return_json_data":{"json_data":"{}"}}}`, "missing_psd_url"},
		{"unsafe_scheme", `{"parameters":{"return_json_data":{"json_data":"{\"image_psd_url\":\"file:///tmp/file.psd\"}"}}}`, "missing_psd_url"},
		{"result_error", `{"parameters":{"return_json_data":{"code":20001,"error_message":"failed"}}}`, "return_json_data_code_20001"},
		{"algorithm_error", `{"data":{"error_code":20001,"error_msg":"failed"}}`, "algorithm_code_20001"},
		{"mtlab_error", `{"mtlab_res":{"error_code":20001,"error_msg":"failed"}}`, "mtlab_code_20001"},
		{"mtlab_legacy_error", `{"mtlab_res":{"ErrorCode":20001,"ErrorMsg":"failed"}}`, "mtlab_code_20001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response statusResponse
			if err := json.Unmarshal([]byte(`{"data":{"status":10,"result":`+tc.result+`}}`), &response); err != nil {
				t.Fatal(err)
			}
			result, err := response.parseResult()
			if err != nil || !result.Failed || result.FailureCode != tc.wantCode || result.PSDURL != "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}
