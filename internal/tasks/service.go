package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/credentials"
	"github.com/98624017/meitu-layering-proxy/internal/meitu"
)

type MeituClient interface {
	Submit(ctx context.Context, credential credentials.Credential, input meitu.SubmitInput) (meitu.SubmitResult, error)
	Status(ctx context.Context, credential credentials.Credential, upstreamTaskID string) (meitu.StatusResult, error)
}

type Service struct {
	store                TaskStore
	pool                 *credentials.Pool
	client               MeituClient
	queueTimeout         time.Duration
	upstreamLeaseTimeout time.Duration
	taskTTL              time.Duration
	maxQueuedTasks       int
	schedulerInterval    time.Duration
	now                  func() time.Time
}

type ServiceOptions struct {
	Store                TaskStore
	Pool                 *credentials.Pool
	Client               MeituClient
	QueueTimeout         time.Duration
	UpstreamLeaseTimeout time.Duration
	TaskTTL              time.Duration
	MaxQueuedTasks       int
	SchedulerInterval    time.Duration
}

func NewService(options ServiceOptions) *Service {
	interval := options.SchedulerInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}

	return &Service{
		store:                options.Store,
		pool:                 options.Pool,
		client:               options.Client,
		queueTimeout:         options.QueueTimeout,
		upstreamLeaseTimeout: options.UpstreamLeaseTimeout,
		taskTTL:              options.TaskTTL,
		maxQueuedTasks:       options.MaxQueuedTasks,
		schedulerInterval:    interval,
		now:                  time.Now,
	}
}

func (s *Service) CreateTask(model string, imageURL string, subjectProtectFlag bool) (*Task, error) {
	now := s.now().UTC()
	id, err := generateTaskID()
	if err != nil {
		return nil, err
	}

	task := &Task{
		ID:                 id,
		Model:              model,
		ImageURL:           imageURL,
		SubjectProtectFlag: subjectProtectFlag,
		Status:             StatusQueued,
		Progress:           0,
		CreatedAt:          now,
		QueueDeadlineAt:    now.Add(s.queueTimeout),
	}
	if err := s.store.CreateIfBelowActiveLimit(task, s.maxQueuedTasks); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) GetTask(id string) (*Task, bool) {
	return s.store.Get(id)
}

func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(s.schedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ProcessOnce(ctx)
		}
	}
}

func (s *Service) ProcessOnce(ctx context.Context) {
	now := s.now().UTC()
	s.failLeaseExpiredTasks(now)
	s.store.DeleteExpired(now, s.taskTTL)

	for _, task := range s.store.ListRunnable(now) {
		select {
		case <-ctx.Done():
			return
		default:
			s.processQueuedTask(ctx, task.ID, now)
		}
	}
}

func (s *Service) RefreshTask(ctx context.Context, id string) (*Task, bool) {
	now := s.now().UTC()
	task, ok := s.store.Get(id)
	if !ok {
		return nil, false
	}

	switch task.Status {
	case StatusQueued, StatusAcquiringCredential:
		s.failQueueExpiredTask(id, now)
		refreshed, exists := s.store.Get(id)
		return refreshed, exists
	case StatusInProgress:
		if s.failLeaseExpiredTask(id, now) {
			refreshed, exists := s.store.Get(id)
			return refreshed, exists
		}
		return s.pollUpstreamStatus(ctx, task, now), true
	default:
		return task, true
	}
}

func (s *Service) processQueuedTask(ctx context.Context, id string, now time.Time) {
	task, ok := s.store.Get(id)
	if !ok || task.Status != StatusQueued {
		return
	}
	if !now.Before(task.QueueDeadlineAt) {
		s.failQueueExpiredTask(id, now)
		return
	}

	triedCredentials := make(map[string]struct{})
	for {
		lease, ok := s.pool.Lease(triedCredentials)
		if !ok {
			_ = s.store.Update(id, func(task *Task) error {
				if task.Status == StatusAcquiringCredential {
					task.Status = StatusQueued
					task.LeasedCredentialName = ""
					task.LeasedCredentialID = ""
				}
				return nil
			})
			return
		}

		shouldSubmit := false
		_ = s.store.Update(id, func(task *Task) error {
			if task.Status != StatusQueued {
				return nil
			}
			if !now.Before(task.QueueDeadlineAt) {
				task.MarkFailed(ErrorQueueTimeoutBeforeUpstream, "任务排队超过 10 分钟，尚未请求上游服务", now)
				return nil
			}
			task.Status = StatusAcquiringCredential
			task.LeasedCredentialName = lease.Credential.Name
			task.LeasedCredentialID = lease.ID
			shouldSubmit = true
			return nil
		})
		if !shouldSubmit {
			s.pool.Release(lease.ID)
			return
		}

		result, err := s.client.Submit(ctx, lease.Credential, meitu.SubmitInput{
			ImageURL:           task.ImageURL,
			SubjectProtectFlag: task.SubjectProtectFlag,
		})
		submittedAt := s.now().UTC()
		if err == nil {
			_ = s.store.Update(id, func(task *Task) error {
				if task.IsTerminal() {
					s.pool.Release(lease.ID)
					return nil
				}
				task.Status = StatusInProgress
				task.Progress = 10
				task.RequestedUpstream = true
				task.UpstreamTaskID = result.UpstreamTaskID
				task.UpstreamStatus = intStatus(result.Status)
				task.UpstreamSubmittedAt = submittedAt
				task.UpstreamLeaseDeadlineAt = submittedAt.Add(s.upstreamLeaseTimeout)
				task.LeasedCredentialName = lease.Credential.Name
				task.LeasedCredentialID = lease.ID
				return nil
			})
			slog.Info("meitu_task_submitted", "task_id", id, "credential", lease.Credential.Name, "upstream_task_id", result.UpstreamTaskID)
			return
		}

		s.pool.Release(lease.ID)
		if meitu.IsRetryableCredentialError(err) {
			triedCredentials[lease.Credential.Name] = struct{}{}
			_ = s.store.Update(id, func(task *Task) error {
				if task.Status == StatusAcquiringCredential {
					task.Status = StatusQueued
					task.LeasedCredentialName = ""
					task.LeasedCredentialID = ""
				}
				return nil
			})
			slog.Warn("meitu_submit_retryable_credential_error", "task_id", id, "credential", lease.Credential.Name, "error", sanitizeError(err))
			continue
		}

		_ = s.store.Update(id, func(task *Task) error {
			if task.IsTerminal() {
				return nil
			}
			task.LeasedCredentialName = ""
			task.LeasedCredentialID = ""
			task.MarkFailed(ErrorUpstreamSubmitFailed, upstreamErrorMessage(err, "任务提交失败"), submittedAt)
			return nil
		})
		slog.Warn("meitu_submit_failed", appendUpstreamErrorAttrs([]any{
			"task_id", id,
			"credential", lease.Credential.Name,
			"error", sanitizeError(err),
		}, err)...)
		return
	}
}

func (s *Service) pollUpstreamStatus(ctx context.Context, task *Task, now time.Time) *Task {
	credential, ok := s.pool.GetCredential(task.LeasedCredentialName)
	if !ok {
		_ = s.store.Update(task.ID, func(current *Task) error {
			if !current.IsTerminal() {
				s.pool.Release(current.LeasedCredentialID)
				current.LeasedCredentialName = ""
				current.LeasedCredentialID = ""
				current.MarkFailed(ErrorInternal, "凭证租约不存在", now)
			}
			return nil
		})
		refreshed, _ := s.store.Get(task.ID)
		return refreshed
	}

	result, err := s.client.Status(ctx, credential, task.UpstreamTaskID)
	observedAt := s.now().UTC()
	if err != nil {
		code := ErrorUpstreamPollFailed
		message := "状态查询失败"
		if meitu.IsProjectJSONParseError(err) {
			code = ErrorProjectJSONParseFailed
			message = "分层工程 JSON 解析失败"
		} else {
			message = upstreamErrorMessage(err, message)
		}
		_ = s.store.Update(task.ID, func(current *Task) error {
			if current.IsTerminal() {
				return nil
			}
			s.pool.Release(current.LeasedCredentialID)
			current.LeasedCredentialName = ""
			current.LeasedCredentialID = ""
			current.MarkFailed(code, message, observedAt)
			return nil
		})
		slog.Warn("meitu_status_failed", appendUpstreamErrorAttrs([]any{
			"task_id", task.ID,
			"error", sanitizeError(err),
		}, err)...)
		refreshed, _ := s.store.Get(task.ID)
		return refreshed
	}

	_ = s.store.Update(task.ID, func(current *Task) error {
		if current.IsTerminal() {
			return nil
		}
		current.UpstreamStatus = intStatus(result.Status)
		current.UpstreamProgress = result.Progress
		if result.Failed {
			s.pool.Release(current.LeasedCredentialID)
			current.LeasedCredentialName = ""
			current.LeasedCredentialID = ""
			message := result.FailureMessage
			if message == "" {
				message = "上游任务失败"
			}
			if result.FailureCode != "" {
				current.UpstreamStatus = result.FailureCode
			}
			current.MarkFailed(ErrorUpstreamFailed, message, observedAt)
			return nil
		}
		if result.Status == meitu.UpstreamStatusCompleted {
			s.pool.Release(current.LeasedCredentialID)
			current.LeasedCredentialName = ""
			current.LeasedCredentialID = ""
			current.MarkCompleted(result.ProjectJSON, result.Width, result.Height, result.LayerCount, observedAt)
			return nil
		}
		current.Status = StatusInProgress
		current.Progress = progressPercent(result.Progress)
		return nil
	})

	refreshed, _ := s.store.Get(task.ID)
	return refreshed
}

func (s *Service) failQueueExpiredTask(id string, now time.Time) bool {
	changed := false
	_ = s.store.Update(id, func(task *Task) error {
		if task.Status != StatusQueued && task.Status != StatusAcquiringCredential {
			return nil
		}
		if now.Before(task.QueueDeadlineAt) {
			return nil
		}
		if task.LeasedCredentialID != "" {
			s.pool.Release(task.LeasedCredentialID)
			task.LeasedCredentialName = ""
			task.LeasedCredentialID = ""
		}
		task.RequestedUpstream = false
		task.MarkFailed(ErrorQueueTimeoutBeforeUpstream, "任务排队超过 10 分钟，尚未请求上游服务", now)
		changed = true
		return nil
	})
	return changed
}

func (s *Service) failLeaseExpiredTasks(now time.Time) {
	for _, task := range s.store.ListUpstreamLeaseExpired(now) {
		s.failLeaseExpiredTask(task.ID, now)
	}
}

func (s *Service) failLeaseExpiredTask(id string, now time.Time) bool {
	changed := false
	_ = s.store.Update(id, func(task *Task) error {
		if task.Status != StatusInProgress || task.UpstreamLeaseDeadlineAt.IsZero() || now.Before(task.UpstreamLeaseDeadlineAt) {
			return nil
		}
		s.pool.Release(task.LeasedCredentialID)
		task.LeasedCredentialName = ""
		task.LeasedCredentialID = ""
		task.MarkFailed(ErrorUpstreamLeaseTimeout, "上游任务超过本地凭证占用保护时间，已释放本地凭证", now)
		changed = true
		return nil
	})
	return changed
}

func intStatus(value int) string {
	return strconv.Itoa(value)
}

func progressPercent(progress float64) int {
	if progress <= 0 {
		return 10
	}
	value := int(math.Round(progress * 100))
	if value < 10 {
		return 10
	}
	if value > 99 {
		return 99
	}
	return value
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeText(err.Error())
}

func sanitizeText(message string) string {
	const maxLen = 300
	if len(message) > maxLen {
		return message[:maxLen]
	}
	return message
}

func upstreamErrorMessage(err error, fallback string) string {
	var upstreamErr *meitu.UpstreamError
	if !errors.As(err, &upstreamErr) {
		return fallback
	}

	detail := strings.TrimSpace(upstreamErr.Message)
	if detail == "" {
		detail = fallback
	}
	detail = sanitizePublicVendorText(detail)
	if detail != fallback {
		detail = fallback + "：" + detail
	}

	diagnostics := make([]string, 0, 2)
	if upstreamErr.Code != "" && upstreamErr.Code != "upstream_error" {
		diagnostics = append(diagnostics, "上游 code: "+sanitizePublicVendorText(upstreamErr.Code))
	}
	if upstreamErr.HTTPStatus > 0 {
		diagnostics = append(diagnostics, fmt.Sprintf("HTTP %d", upstreamErr.HTTPStatus))
	}
	if len(diagnostics) == 0 {
		return detail
	}
	return detail + "（" + strings.Join(diagnostics, "，") + "）"
}

func sanitizePublicVendorText(value string) string {
	replacer := strings.NewReplacer(
		"meituan", "upstream",
		"Meituan", "upstream",
		"MEITUAN", "upstream",
		"meitu", "upstream",
		"Meitu", "upstream",
		"MEITU", "upstream",
		"美团", "上游服务",
		"美图", "上游服务",
	)
	return replacer.Replace(value)
}

func appendUpstreamErrorAttrs(attrs []any, err error) []any {
	var upstreamErr *meitu.UpstreamError
	if !errors.As(err, &upstreamErr) {
		return attrs
	}
	attrs = append(attrs,
		"upstream_http_status", upstreamErr.HTTPStatus,
		"upstream_code", upstreamErr.Code,
		"upstream_message", sanitizeText(upstreamErr.Message),
	)
	if upstreamErr.Body != "" {
		attrs = append(attrs, "upstream_body", sanitizeText(upstreamErr.Body))
	}
	return attrs
}

func generateTaskID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "meitu_task_" + hex.EncodeToString(buffer), nil
}

func SetNowForTest(service *Service, now func() time.Time) {
	if service != nil && now != nil {
		service.now = now
	}
}
