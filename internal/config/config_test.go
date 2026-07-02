package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsAndCredentials(t *testing.T) {
	env := map[string]string{
		"MEITU_PROXY_API_KEY":             "proxy-secret",
		"MEITU_CREDENTIALS_JSON":          `[{"name":"acc1","app_key":"ak","secret_id":"sk"},{"name":"acc2","app_key":"ak2","secret_id":"sk2","app_id":"app","max_concurrency":3}]`,
		"MEITU_QUEUE_TIMEOUT_MS":          "1000",
		"MEITU_UPSTREAM_LEASE_TIMEOUT_MS": "2000",
	}

	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.ProxyAPIKey != "proxy-secret" {
		t.Fatalf("unexpected api key")
	}
	if cfg.Credentials[0].MaxConcurrency != 1 {
		t.Fatalf("default max concurrency = %d, want 1", cfg.Credentials[0].MaxConcurrency)
	}
	if cfg.Credentials[1].MaxConcurrency != 3 {
		t.Fatalf("explicit max concurrency = %d, want 3", cfg.Credentials[1].MaxConcurrency)
	}
	if cfg.Credentials[1].AppID != "app" {
		t.Fatalf("app_id was not preserved")
	}
	if cfg.QueueTimeout != time.Second {
		t.Fatalf("queue timeout = %v, want 1s", cfg.QueueTimeout)
	}
	if cfg.UpstreamLeaseTimeout != 2*time.Second {
		t.Fatalf("lease timeout = %v, want 2s", cfg.UpstreamLeaseTimeout)
	}
	if cfg.MaxQueuedTasks != DefaultMaxQueuedTasks {
		t.Fatalf("max queued tasks = %d, want %d", cfg.MaxQueuedTasks, DefaultMaxQueuedTasks)
	}
}

func TestLoadReadsMaxQueuedTasks(t *testing.T) {
	env := map[string]string{
		"MEITU_PROXY_API_KEY":    "proxy-secret",
		"MEITU_CREDENTIALS_JSON": `[{"name":"acc1","app_key":"ak","secret_id":"sk"}]`,
		"MEITU_MAX_QUEUED_TASKS": "12",
	}

	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.MaxQueuedTasks != 12 {
		t.Fatalf("max queued tasks = %d, want 12", cfg.MaxQueuedTasks)
	}
}

func TestLoadRejectsMissingRequiredFields(t *testing.T) {
	env := map[string]string{
		"MEITU_PROXY_API_KEY":    "proxy-secret",
		"MEITU_CREDENTIALS_JSON": `[{"name":"acc1","app_key":"ak"}]`,
	}

	_, err := Load(func(key string) string { return env[key] })
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if !strings.Contains(err.Error(), "secret_id") {
		t.Fatalf("error = %q, want secret_id", err.Error())
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	env := map[string]string{
		"MEITU_PROXY_API_KEY":    "proxy-secret",
		"MEITU_CREDENTIALS_JSON": `not-json`,
	}

	_, err := Load(func(key string) string { return env[key] })
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("error = %q, want invalid JSON", err.Error())
	}
}
