package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultListenAddr             = ":8080"
	DefaultMaxConcurrency         = 1
	DefaultQueueTimeout           = 10 * time.Minute
	DefaultUpstreamLeaseTimeout   = 30 * time.Minute
	DefaultTaskTTL                = time.Hour
	DefaultMaxQueuedTasks         = 100
	DefaultMeituBaseURL           = "https://openapi.meitu.com"
	DefaultMeituHTTPClientTimeout = 30 * time.Second
)

type Config struct {
	ProxyAPIKey          string
	Credentials          []CredentialConfig
	DefaultConcurrency   int
	QueueTimeout         time.Duration
	UpstreamLeaseTimeout time.Duration
	TaskTTL              time.Duration
	MaxQueuedTasks       int
	ListenAddr           string
	MeituBaseURL         string
	MeituHTTPTimeout     time.Duration
}

type CredentialConfig struct {
	Name           string `json:"name"`
	AppKey         string `json:"app_key"`
	SecretID       string `json:"secret_id"`
	AppID          string `json:"app_id,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

type LookupFunc func(string) string

func LoadFromEnv() (Config, error) {
	return Load(os.Getenv)
}

func Load(getenv LookupFunc) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("getenv is required")
	}

	defaultConcurrency, err := parseIntDefault(getenv("MEITU_DEFAULT_MAX_CONCURRENCY"), DefaultMaxConcurrency)
	if err != nil {
		return Config{}, fmt.Errorf("MEITU_DEFAULT_MAX_CONCURRENCY: %w", err)
	}
	if defaultConcurrency <= 0 {
		defaultConcurrency = DefaultMaxConcurrency
	}

	credentials, err := parseCredentials(getenv("MEITU_CREDENTIALS_JSON"), defaultConcurrency)
	if err != nil {
		return Config{}, err
	}

	queueTimeout, err := parseDurationMSDefault(getenv("MEITU_QUEUE_TIMEOUT_MS"), DefaultQueueTimeout)
	if err != nil {
		return Config{}, fmt.Errorf("MEITU_QUEUE_TIMEOUT_MS: %w", err)
	}
	upstreamLeaseTimeout, err := parseDurationMSDefault(getenv("MEITU_UPSTREAM_LEASE_TIMEOUT_MS"), DefaultUpstreamLeaseTimeout)
	if err != nil {
		return Config{}, fmt.Errorf("MEITU_UPSTREAM_LEASE_TIMEOUT_MS: %w", err)
	}
	taskTTL, err := parseDurationMSDefault(getenv("MEITU_TASK_TTL_MS"), DefaultTaskTTL)
	if err != nil {
		return Config{}, fmt.Errorf("MEITU_TASK_TTL_MS: %w", err)
	}
	httpTimeout, err := parseDurationMSDefault(getenv("MEITU_HTTP_TIMEOUT_MS"), DefaultMeituHTTPClientTimeout)
	if err != nil {
		return Config{}, fmt.Errorf("MEITU_HTTP_TIMEOUT_MS: %w", err)
	}
	maxQueuedTasks, err := parseIntDefault(getenv("MEITU_MAX_QUEUED_TASKS"), DefaultMaxQueuedTasks)
	if err != nil {
		return Config{}, fmt.Errorf("MEITU_MAX_QUEUED_TASKS: %w", err)
	}
	if maxQueuedTasks <= 0 {
		return Config{}, errors.New("MEITU_MAX_QUEUED_TASKS must be greater than 0")
	}

	proxyAPIKey := strings.TrimSpace(getenv("MEITU_PROXY_API_KEY"))
	if proxyAPIKey == "" {
		return Config{}, errors.New("MEITU_PROXY_API_KEY is required")
	}

	listenAddr := strings.TrimSpace(getenv("MEITU_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = DefaultListenAddr
	}
	meituBaseURL := strings.TrimRight(strings.TrimSpace(getenv("MEITU_BASE_URL")), "/")
	if meituBaseURL == "" {
		meituBaseURL = DefaultMeituBaseURL
	}

	return Config{
		ProxyAPIKey:          proxyAPIKey,
		Credentials:          credentials,
		DefaultConcurrency:   defaultConcurrency,
		QueueTimeout:         queueTimeout,
		UpstreamLeaseTimeout: upstreamLeaseTimeout,
		TaskTTL:              taskTTL,
		MaxQueuedTasks:       maxQueuedTasks,
		ListenAddr:           listenAddr,
		MeituBaseURL:         meituBaseURL,
		MeituHTTPTimeout:     httpTimeout,
	}, nil
}

func parseCredentials(raw string, defaultConcurrency int) ([]CredentialConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("MEITU_CREDENTIALS_JSON is required")
	}

	var credentials []CredentialConfig
	if err := json.Unmarshal([]byte(raw), &credentials); err != nil {
		return nil, fmt.Errorf("MEITU_CREDENTIALS_JSON: invalid JSON: %w", err)
	}
	if len(credentials) == 0 {
		return nil, errors.New("MEITU_CREDENTIALS_JSON must contain at least one credential")
	}

	seenNames := make(map[string]struct{}, len(credentials))
	for i := range credentials {
		credentials[i].Name = strings.TrimSpace(credentials[i].Name)
		credentials[i].AppKey = strings.TrimSpace(credentials[i].AppKey)
		credentials[i].SecretID = strings.TrimSpace(credentials[i].SecretID)
		credentials[i].AppID = strings.TrimSpace(credentials[i].AppID)

		if credentials[i].Name == "" {
			return nil, fmt.Errorf("MEITU_CREDENTIALS_JSON[%d].name is required", i)
		}
		if credentials[i].AppKey == "" {
			return nil, fmt.Errorf("MEITU_CREDENTIALS_JSON[%d].app_key is required", i)
		}
		if credentials[i].SecretID == "" {
			return nil, fmt.Errorf("MEITU_CREDENTIALS_JSON[%d].secret_id is required", i)
		}
		if _, ok := seenNames[credentials[i].Name]; ok {
			return nil, fmt.Errorf("MEITU_CREDENTIALS_JSON[%d].name duplicates %q", i, credentials[i].Name)
		}
		seenNames[credentials[i].Name] = struct{}{}

		if credentials[i].MaxConcurrency <= 0 {
			credentials[i].MaxConcurrency = defaultConcurrency
		}
		if credentials[i].MaxConcurrency <= 0 {
			credentials[i].MaxConcurrency = DefaultMaxConcurrency
		}
	}

	return credentials, nil
}

func parseIntDefault(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func parseDurationMSDefault(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, errors.New("must be greater than 0")
	}
	return time.Duration(value) * time.Millisecond, nil
}
