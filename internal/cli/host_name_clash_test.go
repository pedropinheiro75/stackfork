package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `env set` refuses a name that is both a Compose service and a host app.
func TestEnvSetRefusesANameThatIsServiceAndHostApp(t *testing.T) {
	root, stateDir, _, _ := envSetup(t, "services:\n  db:\n    image: postgres:17\n  app:\n    image: busybox\n")
	writeKnowledgeJSON(t, root, `{"schema":2,"plan":{"status":"recorded","steps":[{"id":"up","kind":"compose-up"},{"id":"app","kind":"host","name":"app","cmd":"echo hi"}]}}`)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "env", "set", "app", "FOO=1", "--no-restart")
	if code == 0 || !strings.Contains(stderr, "both a Compose service and a host app") {
		t.Fatalf("code %d: %s", code, stderr)
	}
}

// `config set-mode` on a linked worktree sets the mode of its main checkout.
func TestSetModeOnAWorktreeAppliesToTheMainCheckout(t *testing.T) {
	root := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", "-b", "wt", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	if _, stderr, code := runCLI(t, "config", "set-mode", wt, "hybrid"); code != 0 {
		t.Fatalf("set-mode: %s", stderr)
	}
	out, _, _ := runCLI(t, "config", "get")
	resolved, _ := filepath.EvalSymlinks(root)
	if !strings.Contains(out, resolved+`": "hybrid"`) {
		t.Fatalf("mode not stored under the main checkout %s:\n%s", resolved, out)
	}
}
