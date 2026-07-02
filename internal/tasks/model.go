package tasks

import "time"

const (
	StatusQueued              = "queued"
	StatusAcquiringCredential = "acquiring_credential"
	StatusInProgress          = "in_progress"
	StatusCompleted           = "completed"
	StatusFailed              = "failed"
)

const (
	ErrorQueueTimeoutBeforeUpstream = "meitu_queue_timeout_before_upstream"
	ErrorUpstreamLeaseTimeout       = "meitu_upstream_lease_timeout"
	ErrorUpstreamSubmitFailed       = "meitu_upstream_submit_failed"
	ErrorUpstreamPollFailed         = "meitu_upstream_poll_failed"
	ErrorUpstreamFailed             = "meitu_upstream_failed"
	ErrorProjectJSONParseFailed     = "meitu_project_json_parse_failed"
	ErrorInternal                   = "internal_error"
)

type Task struct {
	ID                      string
	Model                   string
	ImageURL                string
	SubjectProtectFlag      bool
	Status                  string
	Progress                int
	CreatedAt               time.Time
	QueueDeadlineAt         time.Time
	UpstreamSubmittedAt     time.Time
	UpstreamLeaseDeadlineAt time.Time
	CompletedAt             time.Time
	UpstreamTaskID          string
	LeasedCredentialName    string
	LeasedCredentialID      string
	UpstreamStatus          string
	UpstreamProgress        float64
	RequestedUpstream       bool
	ProjectJSON             map[string]any
	Width                   int
	Height                  int
	LayerCount              int
	Error                   *TaskError
}

type TaskError struct {
	Code    string
	Message string
}

func (t Task) IsTerminal() bool {
	return t.Status == StatusCompleted || t.Status == StatusFailed
}

func (t Task) PublicStatus() string {
	switch t.Status {
	case StatusQueued, StatusAcquiringCredential:
		return StatusQueued
	case StatusInProgress:
		return StatusInProgress
	case StatusCompleted:
		return StatusCompleted
	case StatusFailed:
		return StatusFailed
	default:
		return StatusQueued
	}
}

func (t *Task) MarkFailed(code string, message string, now time.Time) {
	t.Status = StatusFailed
	t.Progress = 100
	t.CompletedAt = now
	t.Error = &TaskError{Code: code, Message: message}
}

func (t *Task) MarkCompleted(projectJSON map[string]any, width int, height int, layerCount int, now time.Time) {
	t.Status = StatusCompleted
	t.Progress = 100
	t.CompletedAt = now
	t.ProjectJSON = projectJSON
	t.Width = width
	t.Height = height
	t.LayerCount = layerCount
	t.Error = nil
}
