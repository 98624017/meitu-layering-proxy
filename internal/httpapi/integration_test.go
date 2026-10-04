package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
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

	for _, tc := range []struct {
		name                  string
		editable, synchronous bool
	}{
		{"editable_async", true, false},
		{"non_editable_async", false, false},
		{"editable_sync", true, true},
		{"non_editable_sync", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submitCalls, statusCalls atomic.Int64
			const psdURL = "https://example.com/result.psd"
			projectJSON := `{"width":100,"height":200,"templateConf":[{"id":"layer1"}],"image_psd_url":"` + psdURL + `"}`
			completed := map[string]any{
				"code": 0, "error_code": 0,
				"data": map[string]any{
					"status": 10, "progress": 1, "task_id": "mt-task",
					"result": map[string]any{
						"id":         "mt-task",
						"parameters": map[string]any{"return_json_data": map[string]any{"code": 0, "json_data": projectJSON}},
					},
				},
			}
			fakeMeitu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/sdk/sync/push":
					submitCalls.Add(1)
					if r.Header.Get("Authorization") == "" {
						t.Error("missing upstream authorization")
					}
					var request struct {
						Task       string `json:"task"`
						Params     string `json:"params"`
						InitImages []struct {
							URL string `json:"url"`
						} `json:"init_images"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Fatal(err)
					}
					var params struct {
						Parameter map[string]any `json:"parameter"`
					}
					if err := json.Unmarshal([]byte(request.Params), &params); err != nil {
						t.Fatal(err)
					}
					wantPSD, wantSide := "1", ""
					if !tc.editable {
						wantPSD, wantSide = "0", "text_none_editable"
					}
					if request.Task != "/v1/poster_trans_rob/491768" || len(request.InitImages) != 1 || request.InitImages[0].URL != "https://example.com/input.png" || params.Parameter["convert_json_psd_flag"] != wantPSD || params.Parameter["business_side_flag"] != wantSide || params.Parameter["ori_lang"] != "en" || params.Parameter["subject_protect_flag"] != true || params.Parameter["only_text_eliminate"] != true {
						t.Fatalf("unexpected v2 request: %+v params=%+v", request, params)
					}
					if tc.synchronous {
						_ = json.NewEncoder(w).Encode(completed)
					} else {
						_, _ = w.Write([]byte(`{"code":0,"data":{"status":9,"task_id":"mt-task"}}`))
					}
				case meitu.StatusPath:
					if r.URL.Query().Get("task_id") != "mt-task" {
						t.Fatal("incorrect task_id")
					}
					if statusCalls.Add(1) == 1 {
						_, _ = w.Write([]byte(`{"code":0,"data":{"status":1,"progress":0.5,"task_id":"mt-task"}}`))
					} else {
						_ = json.NewEncoder(w).Encode(completed)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer fakeMeitu.Close()
			pool := credentials.NewPool([]config.CredentialConfig{{Name: "acc1", AppKey: "ak", SecretID: "sk", MaxConcurrency: 1}})
			service := tasks.NewService(tasks.ServiceOptions{
				Store: tasks.NewMemoryStore(), Pool: pool,
				Client:       meitu.NewClient(fakeMeitu.URL, fakeMeitu.Client()),
				QueueTimeout: time.Minute, UpstreamLeaseTimeout: time.Minute, TaskTTL: time.Hour, MaxQueuedTasks: 100,
			})
			server := NewServer(service, auth.NewMiddleware("proxy-secret"))
			handler := server.Handler()
			body, _ := json.Marshal(map[string]any{"model": "layering-v2", "prompt": "分层", "input_reference": "https://example.com/input.png", "text_editable": tc.editable, "ori_lang": "en", "subject_protect_flag": true, "only_text_eliminate": true})
			req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer proxy-secret")
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			var created VideoResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || created.Status != "queued" || created.Model != "layering-v2" || submitCalls.Load() != 0 {
				t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
			}
			service.ProcessOnce(context.Background())
			if submitCalls.Load() != 1 || statusCalls.Load() != 0 {
				t.Fatal("unexpected scheduler upstream calls")
			}
			get := func() VideoResponse {
				req := httptest.NewRequest(http.MethodGet, "/v1/videos/"+created.ID, nil)
				req.Header.Set("Authorization", "Bearer proxy-secret")
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, req)
				var response VideoResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if recorder.Code != http.StatusOK {
					t.Fatalf("GET: %d %s", recorder.Code, recorder.Body.String())
				}
				return response
			}
			if !tc.synchronous {
				waiting := get()
				if waiting.Status != "in_progress" || waiting.Progress != 50 || pool.ActiveCount("acc1") != 1 {
					t.Fatalf("status 1 mishandled: %+v", waiting)
				}
			}
			result := get()
			if result.Status != "completed" || result.Model != "layering-v2" || result.URL != psdURL || result.VideoURL != psdURL || result.ResultURL != psdURL || pool.ActiveCount("acc1") != 0 {
				t.Fatalf("completion: %+v", result)
			}
			metadata := result.Metadata["meitu"].(map[string]any)
			if metadata["psd_url"] != psdURL || metadata["result_format"] != "psd" || metadata["width"] != float64(100) || metadata["height"] != float64(200) || metadata["layer_count"] != float64(1) {
				t.Fatalf("metadata: %+v", metadata)
			}
			if tc.synchronous && statusCalls.Load() != 0 {
				t.Fatal("synchronous result was discarded")
			}
		})
	}
}
