package learn

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

func hasKind(risks []knowledge.Risk, kind string) bool {
	for _, r := range risks {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

// Task 13.4 check: fixture tests for each risk pattern, with file:line.
func TestDetectRisksFixture(t *testing.T) {
	risks := detectRisks(fixture("risky"))

	want := []string{riskResetSeedOnStart, riskWritesEnvOrNpmrc, riskHardcodedComposeP, riskHardcodedExec}
	for _, kind := range want {
		if !hasKind(risks, kind) {
			t.Fatalf("expected a %q risk, got %+v", kind, risks)
		}
	}
	for _, r := range risks {
		if r.File == "" || r.Line == 0 {
			t.Fatalf("expected file:line on every risk, got %+v", r)
		}
	}

	// The manually-invoked "reset-db" Make target is not "on every start",
	// so its own name must not itself trigger reset_or_seed_on_start (only
	// the entrypoint's unconditional seed and the dev script's reset do).
	for _, r := range risks {
		if r.Kind == riskResetSeedOnStart && r.File == "Makefile" {
			t.Fatalf("did not expect reset_or_seed_on_start from a manually-invoked target: %+v", r)
		}
	}
}

func TestDetectEnvKeysGroupedByPurpose(t *testing.T) {
	testsupport.RequireCompose(t)
	k, err := Scan(fixture("risky"))
	if err != nil {
		t.Fatal(err)
	}
	purposeOf := map[string]string{}
	for _, e := range k.EnvKeys {
		purposeOf[e.Name] = e.Purpose
	}
	want := map[string]string{
		"DATABASE_URL": knowledge.PurposeDatabase,
		"AUTH_SECRET":  knowledge.PurposeAuth,
		"SMTP_HOST":    knowledge.PurposeMail,
		"S3_BUCKET":    knowledge.PurposeStorage,
		"NPM_TOKEN":    knowledge.PurposeRegistry,
		"OTHER_FLAG":   knowledge.PurposeOther,
	}
	for name, purpose := range want {
		if purposeOf[name] != purpose {
			t.Fatalf("expected %s to be %q, got %q (all: %+v)", name, purpose, purposeOf[name], k.EnvKeys)
		}
	}
}

// Task 13.4 check (canary): the risky fixture's .env and .npmrc hold unique
// canary values. Scanning it must never surface them anywhere: not in
// knowledge.json, not in knowledge.md, not on stdout or stderr.
func TestCanaryValuesNeverLeak(t *testing.T) {
	testsupport.RequireCompose(t)
	canaries := []string{"CANARY_DB_VALUE_9f8e7d3c", "CANARY_AUTH_VALUE_1a2b3c4d", "CANARY_NPM_TOKEN_7z6y5x4w"}

	root := fixture("risky")
	k, stdout, stderr := scanCapturingOutput(t, root)

	kb, err := jsonBytes(k)
	if err != nil {
		t.Fatal(err)
	}
	md := k.Markdown()

	for _, c := range canaries {
		if strings.Contains(string(kb), c) {
			t.Fatalf("canary %q leaked into knowledge.json", c)
		}
		if strings.Contains(md, c) {
			t.Fatalf("canary %q leaked into knowledge.md", c)
		}
		if strings.Contains(stdout, c) {
			t.Fatalf("canary %q leaked onto stdout", c)
		}
		if strings.Contains(stderr, c) {
			t.Fatalf("canary %q leaked onto stderr", c)
		}
	}
}

// scanCapturingOutput runs Scan while capturing anything written to the
// process's stdout/stderr (docker compose's own suppressed-error path, in
// particular), to prove the canary test covers more than just the return
// value.
func scanCapturingOutput(t *testing.T, root string) (*knowledge.Knowledge, string, string) {
	t.Helper()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW

	k, scanErr := Scan(root)

	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = oldStdout, oldStderr

	var outBuf, errBuf bytes.Buffer
	outBuf.ReadFrom(outR)
	errBuf.ReadFrom(errR)

	if scanErr != nil {
		t.Fatal(scanErr)
	}
	return k, outBuf.String(), errBuf.String()
}

func jsonBytes(k *knowledge.Knowledge) ([]byte, error) {
	path := filepath.Join(os.TempDir(), "stackfork-learn-canary-test.json")
	if err := k.Save(path); err != nil {
		return nil, err
	}
	defer os.Remove(path)
	return os.ReadFile(path)
}

// Task 13.2's .gitignore requirement, checked with real git: a fixture repo
// must ignore runtime.env under .stackfork/.
func TestGitignoreIgnoresRuntimeEnv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	if err := WriteKnowledge(root, &knowledge.Knowledge{Status: knowledge.StatusDraft}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "check-ignore", ".stackfork/runtime.env")
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected .stackfork/runtime.env to be git-ignored: %v", err)
	}
	cmd = exec.Command("git", "-C", root, "check-ignore", ".stackfork/knowledge.json")
	if err := cmd.Run(); err == nil {
		t.Fatal("knowledge.json must not be git-ignored: it is meant to be committed")
	}
}
