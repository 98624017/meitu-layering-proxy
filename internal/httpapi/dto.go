package httpapi

import (
	"encoding/json"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/tasks"
)

type CreateVideoRequest struct {
	Model              string `json:"model"`
	Prompt             string `json:"prompt,omitempty"`
	Image              string `json:"image"`
	InputReference     string `json:"input_reference,omitempty"`
	TextEditable       *bool  `json:"text_editable,omitempty"`
	SubjectProtectFlag *bool  `json:"subject_protect_flag,omitempty"`
	OriLang            string `json:"ori_lang,omitempty"`
	OnlyTextEliminate  bool   `json:"only_text_eliminate,omitempty"`
}

type VideoResponse struct {
	ID          string         `json:"id"`
	TaskID      string         `json:"task_id"`
	Object      string         `json:"object"`
	Model       string         `json:"model"`
	Status      string         `json:"status"`
	Progress    int            `json:"progress"`
	CreatedAt   int64          `json:"created_at"`
	CompletedAt *int64         `json:"completed_at,omitempty"`
	URL         string         `json:"url,omitempty"`
	VideoURL    string         `json:"video_url,omitempty"`
	ResultURL   string         `json:"result_url,omitempty"`
	Error       *APIError      `json:"error,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func BuildVideoResponse(task *tasks.Task) VideoResponse {
	response := VideoResponse{
		ID:        task.ID,
		TaskID:    task.ID,
		Object:    "video",
		Model:     task.Model,
		Status:    task.PublicStatus(),
		Progress:  task.Progress,
		CreatedAt: unixSeconds(task.CreatedAt),
		URL:       task.PSDURL,
		VideoURL:  task.PSDURL,
		ResultURL: task.PSDURL,
	}
	if !task.CompletedAt.IsZero() {
		completedAt := unixSeconds(task.CompletedAt)
		response.CompletedAt = &completedAt
	}
	if task.Error != nil {
		response.Error = &APIError{
			Code:    task.Error.Code,
			Message: task.Error.Message,
		}
	}

	metadata := buildMetadata(task)
	if len(metadata) > 0 {
		response.Metadata = metadata
	}
	return response
}

func buildMetadata(task *tasks.Task) map[string]any {
	meitu := make(map[string]any)
	if !task.QueueDeadlineAt.IsZero() && task.Status == tasks.StatusQueued {
		meitu["queue_started_at"] = unixSeconds(task.CreatedAt)
		meitu["queue_deadline_at"] = unixSeconds(task.QueueDeadlineAt)
	}
	if task.UpstreamTaskID != "" {
		meitu["upstream_task_id"] = task.UpstreamTaskID
	}
	if task.UpstreamStatus != "" {
		meitu["upstream_status"] = task.UpstreamStatus
	}
	if task.UpstreamProgress > 0 {
		meitu["upstream_progress"] = task.UpstreamProgress
	}
	if task.Width > 0 {
		meitu["width"] = task.Width
	}
	if task.Height > 0 {
		meitu["height"] = task.Height
	}
	if task.LayerCount > 0 {
		meitu["layer_count"] = task.LayerCount
	}
	if task.ProjectJSON != nil {
		meitu["project_json"] = task.ProjectJSON
	}
	if task.PSDURL != "" {
		meitu["psd_url"] = task.PSDURL
		meitu["result_format"] = "psd"
	}
	if task.Status == tasks.StatusFailed {
		meitu["requested_upstream"] = task.RequestedUpstream
	}
	if task.UpstreamTaskID != "" || task.UpstreamStatus != "" {
		upstream := make(map[string]any)
		if task.UpstreamTaskID != "" {
			upstream["task_id"] = task.UpstreamTaskID
		}
		if task.UpstreamStatus != "" {
			upstream["status"] = task.UpstreamStatus
		}
		if task.UpstreamProgress > 0 {
			upstream["progress"] = task.UpstreamProgress
		}
		meitu["upstream"] = upstream
	}
	if len(meitu) == 0 {
		return nil
	}
	return map[string]any{"meitu": meitu}
}

func DecodeCreateRequest(decoder *json.Decoder) (CreateVideoRequest, error) {
	var request CreateVideoRequest
	err := decoder.Decode(&request)
	return request, err
}

func unixSeconds(value time.Time) int64 {
	return value.UTC().Unix()
}
