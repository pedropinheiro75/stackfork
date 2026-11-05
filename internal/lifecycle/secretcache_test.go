package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
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
	CacheSecrets(dir, SecretValues{"STRIPE": {Ref: "op://Dev/acme-stripe/test-key", Value: "op-value-123456"}})
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

func gateInfo(t *testing.T, refs map[string]string) *state.Manifest {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	return &state.Manifest{Root: t.TempDir(), Secrets: refs}
}

// Review I4: the dashboard log view must not show a log it cannot redact,
// whatever the reference kind: an unresolved env: or npmrc: ${VAR} (the
// dashboard has no shell environment) or a locked keychain gates like op://.
func TestLogSecretsGateForEveryUnresolvedKind(t *testing.T) {
	npmrc := filepath.Join(t.TempDir(), "npmrc")
	os.WriteFile(npmrc, []byte("//npm.pkg.github.com/:_authToken=${SF_TEST_GATE_NPM}\n"), 0o600)
	locked := testsupport.FakeExe(t, filepath.Join(t.TempDir(), "security"), "#!/bin/sh\necho 'User interaction is not allowed.' >&2\nexit 36\n")
	old := *secretstore.Default
	secretstore.Default.GOOS, secretstore.Default.Security = "darwin", locked
	t.Cleanup(func() { *secretstore.Default = old })
	os.Unsetenv("SF_TEST_GATE_ENV")
	os.Unsetenv("SF_TEST_GATE_NPM")
	for name, tc := range map[string]struct{ ref, want string }{
		"env":      {"env:SF_TEST_GATE_ENV", "shell environment"},
		"npmrc":    {"npmrc:" + npmrc + "#npm.pkg.github.com", "shell environment"},
		"keychain": {"keychain:acme-locked", "Unlock the keychain"},
		"op":       {"op://Dev/acme-stripe/test-key", ""},
	} {
		got, err := LogSecrets(t.TempDir(), gateInfo(t, map[string]string{"TOK": tc.ref}))
		if !errors.Is(err, ErrCheckSecrets) || got != nil {
			t.Errorf("%s: %v %v", name, got, err)
			continue
		}
		if !strings.HasPrefix(err.Error(), ErrCheckSecrets.Error()) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: gate text %q lacks %q", name, err, tc.want)
		}
		if strings.Contains(err.Error(), "acme-locked") || strings.Contains(err.Error(), "SF_TEST_GATE") {
			t.Errorf("%s: gate text names the reference: %q", name, err)
		}
	}
}

// A value remembered for another reference is not this reference's value
// (the map was changed since): the gate shows again.
func TestLogSecretsCacheIsKeyedByReference(t *testing.T) {
	dir := t.TempDir()
	CacheSecrets(dir, SecretValues{"TOK": {Ref: "op://Dev/old/key", Value: "old-value-123456"}})
	if _, err := LogSecrets(dir, gateInfo(t, map[string]string{"TOK": "op://Dev/new/key"})); !errors.Is(err, ErrCheckSecrets) {
		t.Fatalf("a stale value must not count: %v", err)
	}
	got, err := LogSecrets(dir, gateInfo(t, map[string]string{"TOK": "op://Dev/old/key"}))
	if err != nil || len(got) != 1 || got[0].Value != "old-value-123456" {
		t.Fatalf("same reference: %v %v", got, err)
	}
	// and the stale value is never used to redact under the new reference
	CacheSecrets(dir, SecretValues{"TOK": {Ref: "env:SF_TEST_GATE_OLD", Value: "stale-env-value-9"}})
	t.Setenv("SF_TEST_GATE_NEW", "fresh-env-value-9")
	got, err = LogSecrets(dir, gateInfo(t, map[string]string{"TOK": "env:SF_TEST_GATE_NEW"}))
	if err != nil || len(got) != 1 || got[0].Value != "fresh-env-value-9" {
		t.Fatalf("remapped env: %v %v", got, err)
	}
}
