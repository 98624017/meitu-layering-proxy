package credentials

import (
	"testing"

	"github.com/98624017/meitu-layering-proxy/internal/config"
)

func TestPoolHonorsMaxConcurrency(t *testing.T) {
	pool := NewPool([]config.CredentialConfig{{
		Name:           "acc1",
		AppKey:         "ak",
		SecretID:       "sk",
		MaxConcurrency: 1,
	}})

	lease, ok := pool.Lease(nil)
	if !ok {
		t.Fatal("first lease failed")
	}
	if _, ok := pool.Lease(nil); ok {
		t.Fatal("second lease succeeded, want concurrency full")
	}

	if !pool.Release(lease.ID) {
		t.Fatal("release returned false")
	}
	if pool.ActiveCount("acc1") != 0 {
		t.Fatalf("active count = %d, want 0", pool.ActiveCount("acc1"))
	}
}

func TestPoolReleaseIsIdempotent(t *testing.T) {
	pool := NewPool([]config.CredentialConfig{{
		Name:           "acc1",
		AppKey:         "ak",
		SecretID:       "sk",
		MaxConcurrency: 1,
	}})
	lease, ok := pool.Lease(nil)
	if !ok {
		t.Fatal("lease failed")
	}

	pool.Release(lease.ID)
	if pool.Release(lease.ID) {
		t.Fatal("second release returned true")
	}
	if pool.ActiveCount("acc1") != 0 {
		t.Fatalf("active count = %d, want 0", pool.ActiveCount("acc1"))
	}
}

func TestPoolRotatesCredentials(t *testing.T) {
	pool := NewPool([]config.CredentialConfig{
		{Name: "acc1", AppKey: "ak1", SecretID: "sk1", MaxConcurrency: 1},
		{Name: "acc2", AppKey: "ak2", SecretID: "sk2", MaxConcurrency: 1},
	})

	first, ok := pool.Lease(nil)
	if !ok {
		t.Fatal("first lease failed")
	}
	second, ok := pool.Lease(nil)
	if !ok {
		t.Fatal("second lease failed")
	}
	if first.Credential.Name == second.Credential.Name {
		t.Fatalf("pool did not rotate: %q then %q", first.Credential.Name, second.Credential.Name)
	}
}
