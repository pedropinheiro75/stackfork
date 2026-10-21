package lifecycle

import (
	"errors"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

func cacheClock(t *testing.T, now *time.Time) {
	t.Helper()
	old := secretCache.now
	secretCache.now = func() time.Time { return *now }
	t.Cleanup(func() { secretCache.now = old })
}

func TestLogSecretsNeedACheckForOPUntilTheValuesAreRemembered(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	t.Setenv("SF_TEST_LOGSEC_ENV", "env-value-123456")
	dir := t.TempDir()
	info := &state.Manifest{Root: t.TempDir(), Secrets: map[string]string{
		"STRIPE": "op://Dev/acme-stripe/test-key", "PLAIN": "env:SF_TEST_LOGSEC_ENV"}}
	now := time.Now()
	cacheClock(t, &now)

	if _, err := LogSecrets(dir, info); !errors.Is(err, ErrCheckSecrets) {
		t.Fatalf("empty cache with an op:// reference: %v", err)
	}
	CacheSecrets(dir, SecretValues{"STRIPE": "op-value-123456"})
	got, err := LogSecrets(dir, info)
	if err != nil || len(got) != 2 {
		t.Fatalf("remembered: %v %v", got, err)
	}
	now = now.Add(SecretCacheTTL + time.Minute)
	if _, err := LogSecrets(dir, info); !errors.Is(err, ErrCheckSecrets) {
		t.Fatalf("an 8 hour old cache must be gone: %v", err)
	}
}

func TestLogSecretsNeedNoCheckWithoutOP(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	t.Setenv("SF_TEST_LOGSEC_ENV", "env-value-123456")
	info := &state.Manifest{Root: t.TempDir(), Secrets: map[string]string{"PLAIN": "env:SF_TEST_LOGSEC_ENV"}}
	got, err := LogSecrets(t.TempDir(), info)
	if err != nil || len(got) != 1 || got[0].Value != "env-value-123456" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := LogSecrets(t.TempDir(), &state.Manifest{Root: t.TempDir()}); err != nil {
		t.Fatalf("no secrets: %v", err)
	}
}
