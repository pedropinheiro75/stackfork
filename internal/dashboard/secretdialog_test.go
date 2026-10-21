package dashboard_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

// The dialog script never contains a repository or secret name: they travel
// as osascript arguments, so quotes, backslashes and line breaks in a name
// cannot change what the script does.
func TestSecretDialogPassesNamesAsArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "argv.rec")
	stub := filepath.Join(dir, "osascript")
	os.WriteFile(stub, []byte("#!/bin/sh\nfor a in \"$@\"; do printf '[%s]\\n' \"$a\" >> \""+rec+"\"; done\nprintf 'typed-value\\n'\n"), 0o755)
	restore := dashboard.SetOsascriptForTest(stub)
	defer restore()

	hostile := `ac"me\" & (do shell script "x") & "` + "\nweb"
	got, err := dashboard.AskSecretValueNative(hostile, `NAME" & "x`, "acme-npm-token")
	if err != nil || got != "typed-value" {
		t.Fatalf("got %q, %v", got, err)
	}
	b, _ := os.ReadFile(rec)
	argv := string(b)
	script := dashboard.SecretDialogScript
	if strings.Contains(script, "acme") || strings.Contains(script, "NAME") {
		t.Fatal("a name was spliced into the script")
	}
	for _, want := range []string{"hidden answer", "default answer", "gave up"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if !strings.Contains(argv, "keychain:acme-npm-token") || strings.Contains(argv, "typed-value") {
		t.Fatalf("argv: %s", argv)
	}
	if !strings.Contains(argv, `NAME" & "x`) {
		t.Errorf("the secret name must reach the dialog unchanged: %s", argv)
	}
}

func TestSecretDialogCancelIsAnErrorWithoutAValue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	stub := filepath.Join(t.TempDir(), "osascript")
	os.WriteFile(stub, []byte("#!/bin/sh\necho 'User canceled. (-128)' >&2\nexit 1\n"), 0o755)
	defer dashboard.SetOsascriptForTest(stub)()
	if v, err := dashboard.AskSecretValueNative("acme-web", "NPM_TOKEN", "acme-npm-token"); err == nil || v != "" {
		t.Fatalf("cancel: %q, %v", v, err)
	}
}

// The script must at least compile; the real dialog needs a person.
func TestSecretDialogScriptCompiles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	osacompile, err := exec.LookPath("osacompile")
	if err != nil {
		t.Skip("osacompile not found")
	}
	out := filepath.Join(t.TempDir(), "d.scpt")
	if b, err := exec.Command(osacompile, "-e", dashboard.SecretDialogScript, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
}
