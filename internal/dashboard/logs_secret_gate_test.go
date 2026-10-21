package dashboard_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// D11: with an op:// reference and no remembered values, the log view shows
// no log (it would show the value) until a check the person starts.
func TestLogViewWaitsForASecretCheckWhenOPValuesAreUnknown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tool")
	}
	newBase, legacyBase, project := preparedFixture(t)
	const canary = "op-canary-dashboard-4-3"
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	tool := filepath.Join(dir, "op")
	os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s' '"+canary+"'\n"), 0o755)
	old := *secretstore.Default
	secretstore.Default.OpPaths = []string{tool}
	t.Cleanup(func() { *secretstore.Default = old })

	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	mapTestSecret(t, m.Root, "tok", "op://Dev/acme-stripe/test-key") // a --secret flag takes only env: and npmrc:
	if code := cli.Run([]string{"--root", m.Root, "--state-dir", newBase,
		"prepare", "--force", "-f", filepath.Join(m.Root, "compose.yaml")}, &out, &errBuf); code != 0 {
		t.Fatal(errBuf.String())
	}
	t.Setenv("DOCKER_FAKE_LOGS", "app-1  | key "+canary+"\n")
	srv, err := dashboard.New(newBase, legacyBase, "test", 55411)
	if err != nil {
		t.Fatal(err)
	}
	rec := getLogs(t, srv, "127.0.0.1:55411", project, "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "Check secrets to view logs") || strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("gate: %d %q", rec.Code, rec.Body.String())
	}
	m, _ = state.ReadManifest(filepath.Join(newBase, project, "manifest.json")) // now with tok
	if _, err := lifecycle.CheckSecrets(filepath.Join(newBase, project), m); err != nil {
		t.Fatal(err)
	}
	rec = getLogs(t, srv, "127.0.0.1:55411", project, "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), canary) || !strings.Contains(rec.Body.String(), "[redacted:tok]") {
		t.Fatalf("after the check: %d %q", rec.Code, rec.Body.String())
	}
}

// Review I4: an env: reference the dashboard cannot see gates the log view
// too, with the way out in the body; it used to show the log unredacted.
func TestLogViewGatesAnUnresolvedEnvReference(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	const val = "env-canary-dashboard-9-9"
	t.Setenv("SF_TEST_DASH_ENV", val)
	var out, errBuf bytes.Buffer
	if code := cli.Run([]string{"--root", m.Root, "--state-dir", newBase, "--secret", "tok=env:SF_TEST_DASH_ENV",
		"prepare", "--force", "-f", filepath.Join(m.Root, "compose.yaml")}, &out, &errBuf); code != 0 {
		t.Fatal(errBuf.String())
	}
	os.Unsetenv("SF_TEST_DASH_ENV") // the dashboard process has no shell environment
	t.Setenv("DOCKER_FAKE_LOGS", "app-1  | key "+val+"\n")
	srv, err := dashboard.New(newBase, legacyBase, "test", 55412)
	if err != nil {
		t.Fatal(err)
	}
	rec := getLogs(t, srv, "127.0.0.1:55412", project, "")
	body := rec.Body.String()
	if rec.Code != 409 || rec.Header().Get("X-Stackfork-Needs") != "secret-check" || strings.Contains(body, val) ||
		!strings.Contains(body, "Check secrets to view logs") || !strings.Contains(body, "shell environment") || !strings.Contains(body, "keychain") {
		t.Fatalf("gate: %d %q", rec.Code, body)
	}
}
