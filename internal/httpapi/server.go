package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/98624017/meitu-layering-proxy/internal/auth"
	"github.com/98624017/meitu-layering-proxy/internal/tasks"
)

const maxRequestBodyBytes = 1 << 20

type TaskService interface {
	CreateTask(model string, imageURL string, subjectProtectFlag bool) (*tasks.Task, error)
	RefreshTask(ctx context.Context, id string) (*tasks.Task, bool)
}

type Server struct {
	taskService TaskService
	auth        auth.Middleware
}

func NewServer(taskService TaskService, authMiddleware auth.Middleware) *Server {
	return &Server{
		taskService: taskService,
		auth:        authMiddleware,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/v1/videos", s.auth.Wrap(http.HandlerFunc(s.handleVideos)))
	mux.Handle("/v1/videos/", s.auth.Wrap(http.HandlerFunc(s.handleVideoTask)))
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVideos(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/videos" {
		WriteError(w, http.StatusNotFound, CodeTaskNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if !hasJSONContentType(r.Header.Get("Content-Type")) {
		WriteError(w, http.StatusUnsupportedMediaType, CodeUnsupportedContentType, "Content-Type 必须是 application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	request, err := DecodeCreateRequest(decoder)
	if err != nil {
		if errors.Is(err, io.EOF) {
			WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "请求体不能为空")
			return
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			WriteError(w, http.StatusRequestEntityTooLarge, CodeRequestBodyTooLarge, "请求体过大")
			return
		}
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "请求体不是有效 JSON")
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "请求体只能包含一个 JSON 对象")
		return
	}

	model, imageURL, subjectProtectFlag, err := ValidateCreateRequest(request)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}

	task, err := s.taskService.CreateTask(model, imageURL, subjectProtectFlag)
	if err != nil {
		if errors.Is(err, tasks.ErrTaskQueueFull) {
			WriteError(w, http.StatusTooManyRequests, CodeTaskQueueFull, "美图代理任务队列已满，请稍后重试")
			return
		}
		slog.Error("create_task_failed", "error", err)
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "创建任务失败")
		return
	}
	WriteJSON(w, http.StatusOK, BuildVideoResponse(task))
}

func (s *Server) handleVideoTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/v1/videos/")
	if taskID == "" || strings.Contains(taskID, "/") {
		WriteError(w, http.StatusNotFound, CodeTaskNotFound, "任务不存在")
		return
	}

	task, ok := s.taskService.RefreshTask(r.Context(), taskID)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeTaskNotFound, "任务不存在")
		return
	}
	WriteJSON(w, http.StatusOK, BuildVideoResponse(task))
}

func hasJSONContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	contentType = strings.ToLower(contentType)
	return contentType == "application/json" || strings.HasPrefix(contentType, "application/json;")
}
