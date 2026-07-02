package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

const (
	CodeInvalidRequest             = "invalid_request"
	CodeUnauthorized               = "unauthorized"
	CodeTaskNotFound               = "task_not_found"
	CodeTaskQueueFull              = "meitu_task_queue_full"
	CodeQueueTimeoutBeforeUpstream = "meitu_queue_timeout_before_upstream"
	CodeUpstreamLeaseTimeout       = "meitu_upstream_lease_timeout"
	CodeUpstreamSubmitFailed       = "meitu_upstream_submit_failed"
	CodeUpstreamPollFailed         = "meitu_upstream_poll_failed"
	CodeUpstreamFailed             = "meitu_upstream_failed"
	CodeProjectJSONParseFailed     = "meitu_project_json_parse_failed"
	CodeCredentialConcurrencyFull  = "meitu_credential_concurrency_full"
	CodeCredentialRateLimited      = "meitu_credential_rate_limited"
	CodeCredentialUnavailable      = "meitu_credential_unavailable"
	CodeInternalError              = "internal_error"
	CodeRequestBodyTooLarge        = "request_body_too_large"
	CodeUnsupportedContentType     = "unsupported_content_type"
	CodeMethodNotAllowed           = "method_not_allowed"
)

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

func WriteJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write_json_failed", "error", err)
	}
}

func WriteError(w http.ResponseWriter, statusCode int, code string, message string) {
	WriteJSON(w, statusCode, ErrorResponse{
		Error: APIError{
			Code:    code,
			Message: message,
		},
	})
}
