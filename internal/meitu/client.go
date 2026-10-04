package meitu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/credentials"
)

const (
	SubmitPath   = "/api/v1/sdk/sync/push"
	StatusPath   = "/api/v1/sdk/status"
	LayeringTask = "/v1/poster_trans_rob/491768"

	UpstreamStatusCompleted = 10
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
		now:        time.Now,
	}
}

func (c *Client) Submit(ctx context.Context, credential credentials.Credential, input SubmitInput) (SubmitResult, error) {
	businessSide, psdFlag := "", "1"
	if !input.Options.TextEditable {
		businessSide, psdFlag = "text_none_editable", "0"
	}
	params, err := json.Marshal(map[string]any{
		"rsp_media_type": "url",
		"parameter": map[string]any{
			"eliminate_type":        "big",
			"only_text_eliminate":   input.Options.OnlyTextEliminate,
			"ori_lang":              input.Options.OriLang,
			"poster_translate_flag": "9",
			"subject_protect_flag":  input.Options.SubjectProtectFlag,
			"target_lang":           "ch",
			"business_side_flag":    businessSide,
			"generate_picture_flag": "0",
			"convert_json_psd_flag": psdFlag,
		},
	})
	if err != nil {
		return SubmitResult{}, fmt.Errorf("marshal layering parameters: %w", err)
	}
	body := map[string]any{
		"task":      LayeringTask,
		"task_type": "formula",
		"init_images": []any{map[string]any{
			"url": input.ImageURL,
			"profile": map[string]any{
				"media_profiles": map[string]string{"media_data_type": "url"},
				"version":        "v1",
			},
		}},
		"params":       string(params),
		"sync_timeout": 1,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("marshal submit request: %w", err)
	}

	endpoint := c.baseURL + SubmitPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return SubmitResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.sign(req, credential, string(bodyBytes)); err != nil {
		return SubmitResult{}, err
	}

	responseBytes, statusCode, err := c.do(req)
	if err != nil {
		return SubmitResult{}, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return SubmitResult{}, newHTTPUpstreamError(statusCode, "提交接口返回 HTTP %d", responseBytes)
	}

	var response statusResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return SubmitResult{}, NewUpstreamError(statusCode, "invalid_response", "提交响应无法解析", responseBytes)
	}
	if response.HasError() {
		return SubmitResult{}, NewUpstreamError(statusCode, response.NormalizedCode(), response.NormalizedMessage("任务提交失败"), responseBytes)
	}
	upstreamTaskID := strings.TrimSpace(response.Data.TaskID)
	if upstreamTaskID == "" {
		upstreamTaskID = strings.TrimSpace(response.Data.Result.ID)
	}
	if upstreamTaskID == "" {
		return SubmitResult{}, NewUpstreamError(statusCode, "missing_task_id", "提交响应缺少任务 ID", responseBytes)
	}

	result := SubmitResult{
		UpstreamTaskID: upstreamTaskID,
		Status:         response.Data.Status,
	}
	if !isInProgressStatus(response.Data.Status) {
		terminal, err := response.parseResult()
		if err != nil {
			return SubmitResult{}, err
		}
		result.Result = &terminal
	}
	return result, nil
}

func (c *Client) Status(ctx context.Context, credential credentials.Credential, upstreamTaskID string) (StatusResult, error) {
	values := url.Values{}
	values.Set("task_id", upstreamTaskID)
	endpoint := c.baseURL + StatusPath + "?" + values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return StatusResult{}, err
	}
	req.Header.Set("X-Sdk-Content-Sha256", "UNSIGNED-PAYLOAD")
	if err := c.sign(req, credential, ""); err != nil {
		return StatusResult{}, err
	}

	responseBytes, statusCode, err := c.do(req)
	if err != nil {
		return StatusResult{}, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return StatusResult{}, newHTTPUpstreamError(statusCode, "状态接口返回 HTTP %d", responseBytes)
	}

	var response statusResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return StatusResult{}, NewUpstreamError(statusCode, "invalid_response", "状态响应无法解析", responseBytes)
	}
	if response.HasError() {
		return StatusResult{}, NewUpstreamError(statusCode, response.NormalizedCode(), response.NormalizedMessage("状态查询失败"), responseBytes)
	}

	return response.parseResult()
}

func (response statusResponse) parseResult() (StatusResult, error) {
	result := StatusResult{
		Status:   response.Data.Status,
		Progress: response.Data.Progress,
	}
	if response.Data.TaskID != "" {
		result.UpstreamTaskID = response.Data.TaskID
	} else if response.Data.Result.ID != "" {
		result.UpstreamTaskID = response.Data.Result.ID
	}

	if isInProgressStatus(response.Data.Status) {
		return result, nil
	}
	if response.Data.Status != UpstreamStatusCompleted {
		result.Failed = true
		result.FailureCode = fmt.Sprintf("status_%d", response.Data.Status)
		result.FailureMessage = fmt.Sprintf("上游任务失败，状态码 %d", response.Data.Status)
		return result, nil
	}

	returnJSONData := response.Data.Result.Parameters.ReturnJSONData
	for _, failure := range []struct {
		code            int
		message, source string
	}{
		{returnJSONData.Code, returnJSONData.ErrorMessage, "return_json_data"},
		{response.Data.Result.Data.ErrorCode, response.Data.Result.Data.ErrorMsg, "algorithm"},
		{response.Data.Result.MTLab.ErrorCode, response.Data.Result.MTLab.ErrorMsg, "mtlab"},
		{response.Data.Result.MTLab.LegacyCode, response.Data.Result.MTLab.LegacyMessage, "mtlab"},
	} {
		if failure.code != 0 {
			result.Failed = true
			result.FailureCode = fmt.Sprintf("%s_code_%d", failure.source, failure.code)
			result.FailureMessage = strings.TrimSpace(failure.message)
			if result.FailureMessage == "" {
				result.FailureMessage = "分层结果返回业务错误"
			}
			return result, nil
		}
	}

	projectJSONText := returnJSONData.JSONData
	if strings.TrimSpace(projectJSONText) == "" {
		return StatusResult{}, NewProjectJSONParseError("完成响应缺少 project json")
	}
	var projectJSON map[string]any
	if err := json.Unmarshal([]byte(projectJSONText), &projectJSON); err != nil || projectJSON == nil {
		return StatusResult{}, NewProjectJSONParseError("project json 无法解析")
	}
	psdURL, _ := projectJSON["image_psd_url"].(string)
	parsedURL, err := url.Parse(strings.TrimSpace(psdURL))
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		result.Failed = true
		result.FailureCode = "missing_psd_url"
		result.FailureMessage = "上游任务完成，但未返回有效的 PSD 下载地址"
		return result, nil
	}
	width, height, layerCount := summarizeProject(projectJSON)
	result.ProjectJSON = projectJSON
	result.PSDURL = parsedURL.String()
	result.Width = width
	result.Height = height
	result.LayerCount = layerCount
	result.Progress = 1

	return result, nil
}

func (c *Client) do(req *http.Request) ([]byte, int, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (c *Client) sign(req *http.Request, credential credentials.Credential, body string) error {
	if credential.AppKey == "" || credential.SecretID == "" {
		return errors.New("credential app_key and secret_id are required")
	}
	if req.URL == nil {
		return errors.New("request URL is required")
	}

	now := c.now().UTC().Format("20060102T150405Z")
	req.Header.Set("X-Sdk-Date", now)
	req.Header.Set("Host", req.URL.Host)
	req.Host = req.URL.Host

	headers := make(map[string]string)
	for key, values := range req.Header {
		if len(values) == 0 {
			continue
		}
		headers[key] = values[0]
	}
	if req.Host != "" {
		headers["Host"] = req.Host
	}

	signedHeaders := signedHeaderNames(headers)
	canonicalRequest := canonicalRequest(req.Method, req.URL, headers, body, signedHeaders)
	stringToSign := stringToSign(canonicalRequest, now)

	mac := hmac.New(sha256.New, []byte(credential.SecretID))
	if _, err := mac.Write([]byte(stringToSign)); err != nil {
		return err
	}
	signature := hex.EncodeToString(mac.Sum(nil))

	headerValue := fmt.Sprintf(
		"SDK-HMAC-SHA256 Access=%s, SignedHeaders=%s, Signature=%s",
		credential.AppKey,
		strings.Join(signedHeaders, ";"),
		signature,
	)
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(headerValue)))
	return nil
}

func signedHeaderNames(headers map[string]string) []string {
	result := make([]string, 0, len(headers))
	for key := range headers {
		result = append(result, strings.ToLower(key))
	}
	sort.Strings(result)
	return result
}

func canonicalRequest(method string, parsedURL *url.URL, headers map[string]string, body string, signedHeaders []string) string {
	path := parsedURL.EscapedPath()
	if path == "" || !strings.HasSuffix(path, "/") {
		path += "/"
	}

	queryValues := parsedURL.Query()
	queryKeys := make([]string, 0, len(queryValues))
	for key := range queryValues {
		queryKeys = append(queryKeys, key)
	}
	sort.Strings(queryKeys)
	encodedQuery := make(url.Values, len(queryValues))
	for _, key := range queryKeys {
		values := append([]string(nil), queryValues[key]...)
		sort.Strings(values)
		encodedQuery[key] = values
	}

	lowerHeaders := make(map[string]string, len(headers))
	for key, value := range headers {
		lowerHeaders[strings.ToLower(key)] = strings.TrimSpace(value)
	}

	canonicalHeaderParts := make([]string, 0, len(signedHeaders))
	for _, key := range signedHeaders {
		canonicalHeaderParts = append(canonicalHeaderParts, key+":"+lowerHeaders[key])
	}

	payloadHash := lowerHeaders[strings.ToLower("X-Sdk-Content-Sha256")]
	if payloadHash == "" {
		hash := sha256.Sum256([]byte(body))
		payloadHash = hex.EncodeToString(hash[:])
	}

	return strings.Join([]string{
		method,
		path,
		encodedQuery.Encode(),
		strings.Join(canonicalHeaderParts, "\n"),
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")
}

func stringToSign(canonicalRequest string, sdkDate string) string {
	hash := sha256.Sum256([]byte(canonicalRequest))
	return strings.Join([]string{
		"SDK-HMAC-SHA256",
		sdkDate,
		hex.EncodeToString(hash[:]),
	}, "\n")
}

func summarizeProject(projectJSON map[string]any) (int, int, int) {
	layers, _ := projectJSON["templateConf"].([]any)
	return numberToInt(projectJSON["width"]), numberToInt(projectJSON["height"]), len(layers)
}

func isInProgressStatus(status int) bool {
	return status == 0 || status == 1 || status == 9
}

func numberToInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	default:
		return 0
	}
}

type SubmitInput struct {
	ImageURL string
	Options  LayeringOptions
}

type LayeringOptions struct {
	TextEditable       bool   `json:"text_editable"`
	SubjectProtectFlag bool   `json:"subject_protect_flag"`
	OriLang            string `json:"ori_lang"`
	OnlyTextEliminate  bool   `json:"only_text_eliminate"`
}

type SubmitResult struct {
	UpstreamTaskID string
	Status         int
	Result         *StatusResult
}

type StatusResult struct {
	UpstreamTaskID string
	Status         int
	Progress       float64
	ProjectJSON    map[string]any
	PSDURL         string
	Width          int
	Height         int
	LayerCount     int
	Failed         bool
	FailureCode    string
	FailureMessage string
}

type UpstreamError struct {
	HTTPStatus int
	Code       string
	Message    string
	Body       string
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewUpstreamError(httpStatus int, code string, message string, body []byte) *UpstreamError {
	return &UpstreamError{
		HTTPStatus: httpStatus,
		Code:       normalizeCode(code),
		Message:    strings.TrimSpace(message),
		Body:       truncateBody(body),
	}
}

func newHTTPUpstreamError(httpStatus int, fallbackFormat string, body []byte) *UpstreamError {
	code := "http_error"
	message := fmt.Sprintf(fallbackFormat, httpStatus)
	if parsed, ok := parseCommonResponseError(body); ok {
		code = parsed.NormalizedCode(code)
		message = parsed.NormalizedMessage(message)
	}
	return NewUpstreamError(httpStatus, code, message, body)
}

func parseCommonResponseError(body []byte) (commonResponseError, bool) {
	var parsed commonResponseError
	if err := json.Unmarshal(body, &parsed); err != nil {
		return commonResponseError{}, false
	}
	return parsed, true
}

type ProjectJSONParseError struct {
	Message string
}

func (e *ProjectJSONParseError) Error() string {
	return e.Message
}

func NewProjectJSONParseError(message string) *ProjectJSONParseError {
	return &ProjectJSONParseError{Message: message}
}

func IsProjectJSONParseError(err error) bool {
	var target *ProjectJSONParseError
	return errors.As(err, &target)
}

func IsRetryableCredentialError(err error) bool {
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	switch upstreamErr.HTTPStatus {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	code := strings.ToLower(upstreamErr.Code)
	return strings.Contains(code, "limit") ||
		strings.Contains(code, "rate") ||
		strings.Contains(code, "concurrency") ||
		strings.Contains(code, "busy") ||
		strings.Contains(code, "account") ||
		strings.Contains(code, "credential")
}

func normalizeCode(code string) string {
	code = strings.TrimSpace(strings.ToLower(code))
	if code == "" {
		return "upstream_error"
	}
	replacer := strings.NewReplacer(" ", "_", "-", "_", ".", "_", ":", "_")
	code = replacer.Replace(code)
	return code
}

func truncateBody(body []byte) string {
	const maxBodyBytes = 2048
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes]
	}
	return string(body)
}

type statusResponse struct {
	RequestID    string `json:"request_id"`
	TraceID      string `json:"trace_id"`
	Code         int    `json:"code"`
	ErrorCode    int    `json:"error_code"`
	Message      string `json:"message"`
	Error        string `json:"error"`
	ErrorMsg     string `json:"error_msg"`
	ErrorMessage string `json:"error_message"`
	Data         struct {
		TaskID   string  `json:"task_id"`
		Status   int     `json:"status"`
		Progress float64 `json:"progress"`
		Result   struct {
			ID    string              `json:"id"`
			Data  commonResponseError `json:"data"`
			MTLab struct {
				ErrorCode     int    `json:"error_code"`
				ErrorMsg      string `json:"error_msg"`
				LegacyCode    int    `json:"ErrorCode"`
				LegacyMessage string `json:"ErrorMsg"`
			} `json:"mtlab_res"`
			Parameters struct {
				ReturnJSONData struct {
					Code         int    `json:"code"`
					CostTime     string `json:"cost_time"`
					ErrorMessage string `json:"error_message"`
					JSONData     string `json:"json_data"`
				} `json:"return_json_data"`
			} `json:"parameters"`
		} `json:"result"`
	} `json:"data"`
}

func (r statusResponse) HasError() bool {
	return r.Code != 0 ||
		r.ErrorCode != 0 ||
		strings.TrimSpace(r.Error) != "" ||
		strings.TrimSpace(r.ErrorMsg) != "" ||
		strings.TrimSpace(r.ErrorMessage) != ""
}

func (r statusResponse) NormalizedCode() string {
	if r.ErrorCode != 0 {
		return fmt.Sprintf("error_code_%d", r.ErrorCode)
	}
	if r.Code != 0 {
		return fmt.Sprintf("code_%d", r.Code)
	}
	if r.Error != "" {
		return r.Error
	}
	if r.Message != "" {
		return r.Message
	}
	return "upstream_error"
}

func (r statusResponse) NormalizedMessage(fallback string) string {
	if strings.TrimSpace(r.ErrorMessage) != "" {
		return r.ErrorMessage
	}
	if strings.TrimSpace(r.ErrorMsg) != "" {
		return r.ErrorMsg
	}
	if strings.TrimSpace(r.Error) != "" {
		return r.Error
	}
	if strings.TrimSpace(r.Message) != "" {
		return r.Message
	}
	return fallback
}

type commonResponseError struct {
	Code         int    `json:"code"`
	ErrorCode    int    `json:"error_code"`
	Message      string `json:"message"`
	Error        string `json:"error"`
	ErrorMsg     string `json:"error_msg"`
	ErrorMessage string `json:"error_message"`
	Msg          string `json:"msg"`
}

func (r commonResponseError) NormalizedCode(fallback string) string {
	if r.ErrorCode != 0 {
		return fmt.Sprintf("error_code_%d", r.ErrorCode)
	}
	if r.Code != 0 {
		return fmt.Sprintf("code_%d", r.Code)
	}
	if strings.TrimSpace(r.Error) != "" {
		return r.Error
	}
	return fallback
}

func (r commonResponseError) NormalizedMessage(fallback string) string {
	for _, candidate := range []string{r.ErrorMessage, r.ErrorMsg, r.Error, r.Message, r.Msg} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return fallback
}

func CloneProjectJSON(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	return maps.Clone(value)
}
