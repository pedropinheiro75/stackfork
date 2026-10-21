package runs

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/redact"
)

func TestStepWriterRedactsBothOutputs(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	secret := "s3cr3t-token-value"
	r, _ := newRun(dir, Meta{Command: "up", Out: &out,
		Secrets: func() []redact.Secret { return []redact.Secret{{Name: "npm_token", Value: secret}} }}, 1)
	s := r.Step("seed", "exec", "x")
	w := s.Start()
	w.Write([]byte("token=" + secret + " b64=" + base64.StdEncoding.EncodeToString([]byte(secret)) + "\n"))
	s.End(nil, 0)
	log, _ := os.ReadFile(filepath.Join(dir, "runs", "1", "seed.log"))
	for name, got := range map[string]string{"out": out.String(), "log": string(log)} {
		if strings.Contains(got, secret) || strings.Contains(got, base64.StdEncoding.EncodeToString([]byte(secret))) {
			t.Errorf("%s leaks: %q", name, got)
		}
		if strings.Count(got, "[redacted:npm_token]") != 2 {
			t.Errorf("%s = %q", name, got)
		}
	}
}

func TestStepLogCap(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	r, _ := newRun(dir, Meta{Command: "up", Out: &out}, 1)
	s := r.Step("big", "exec", "x")
	w := s.Start()
	line := strings.Repeat("x", 99) + "\n"
	n := 5 << 20 / len(line)
	for i := 0; i < n; i++ {
		w.Write([]byte(line))
	}
	w.Write([]byte("LAST LINE\n"))
	s.End(nil, 0)
	log, _ := os.ReadFile(filepath.Join(dir, "runs", "1", "big.log"))
	if len(log) > 1<<20+100 || len(log) < 1<<20 {
		t.Errorf("log = %d bytes", len(log))
	}
	if !strings.Contains(string(log), "bytes not kept ...]\n") || !strings.HasSuffix(string(log), "LAST LINE\n") {
		t.Error("marker or last line missing")
	}
	if !strings.HasPrefix(string(log), line) {
		t.Error("head missing")
	}
	if out.Len() < n*len(line) {
		t.Errorf("caller output must stay complete, got %d", out.Len())
	}
}

// A failed step's log ends with why it failed, redacted, so the card's tail
// and View log show the error and not only the last progress line.
func TestFailedStepLogEndsWithTheError(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	secret := "s3cr3t-token-value"
	r, _ := newRun(dir, Meta{Command: "reset", Out: &out,
		Secrets: func() []redact.Secret { return []redact.Secret{{Name: "npm_token", Value: secret}} }}, 1)
	s := r.Step("prepare", "prepare", "x")
	s.Start().Write([]byte("preparing it"))
	s.End(errors.New("env_file must reference only --env-file; token "+secret), 1)
	log, _ := os.ReadFile(filepath.Join(dir, "runs", "1", "prepare.log"))
	if !strings.HasSuffix(string(log), "preparing it\nerror: env_file must reference only --env-file; token [redacted:npm_token]\n") {
		t.Fatalf("log = %q", log)
	}
	if strings.Contains(out.String(), "error:") {
		t.Errorf("the caller already shows the returned error: %q", out.String())
	}
}
