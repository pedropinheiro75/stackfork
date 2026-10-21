package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// instanceManifestSecrets is the manifest references of the instance of path.
func instanceManifestSecrets(t *testing.T, base []string, path string) map[string]string {
	t.Helper()
	whereOut, _, _ := runCLI(t, append(append([]string{}, base...), "--root", path, "where")...)
	var w struct{ State string }
	json.Unmarshal([]byte(whereOut), &w)
	m, err := os.ReadFile(filepath.Join(w.State, "manifest.json"))
	if err != nil {
		t.Fatalf("%v\n%s", err, whereOut)
	}
	var man struct{ Secrets map[string]string }
	json.Unmarshal(m, &man)
	return man.Secrets
}

func newWorktree(t *testing.T, repo, name string) string {
	t.Helper()
	nw := filepath.Join(repo, ".worktrees", name)
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", name, nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	return nw
}

// Review I1: the preflight and prepare use one reference set. Two siblings:
// feat-a (older) has a token, feat-d (newer) has the usable recipe and none.
// The old preflight took feat-a's token while prepare took feat-d's none.
func TestStartEnvPreflightAndPrepareUseTheSameSiblingSet(t *testing.T) {
	repo, _, base := siblingRepo(t)
	installFakeDocker(t)
	time.Sleep(20 * time.Millisecond)
	if _, e, code := runCLI(t, append(append([]string{}, base...), "worktree", "add", repo, "feat-d", "--from", "main")...); code != 0 {
		t.Fatal(e)
	}
	fd := filepath.Join(repo, ".worktrees", "feat-d")
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", fd, "prepare", "-f", "docker-compose.yml")...); code != 0 {
		t.Fatal(e)
	}
	nw := newWorktree(t, repo, "feat-b")
	out, e, code := runCLI(t, append(append([]string{}, base...), "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if got := instanceManifestSecrets(t, base, nw); len(got) != 0 {
		t.Errorf("prepare recorded %v", got)
	}
	if b, err := runs.Log(runsInstance(t, base[1]), 1, "secrets"); err == nil && strings.Contains(string(b), "tok") {
		t.Errorf("the preflight resolved a reference prepare did not record:\n%s", b)
	}
}

// D7: a --secret flag of the command beats a sibling's reference of the same
// name, in the preflight and in what prepare records.
func TestStartEnvUserSecretFlagBeatsSiblingReference(t *testing.T) {
	repo, _, base := siblingRepo(t)
	installFakeDocker(t)
	t.Setenv("SF_TEST_USER_TOKEN", "user-value-1234")
	nw := newWorktree(t, repo, "feat-b")
	out, e, code := runCLI(t, append(append([]string{}, base...), "--secret", "tok=env:SF_TEST_USER_TOKEN", "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if got := instanceManifestSecrets(t, base, nw)["tok"]; got != "env:SF_TEST_USER_TOKEN" {
		t.Errorf("manifest tok = %q", got)
	}
}

// Review I1.3: under a run, Execute resolves nothing the preflight did not;
// a reference missing from the set or different from it fails closed.
func TestExecuteUnderARunRefusesALateResolve(t *testing.T) {
	installFakeDocker(t)
	root, stateDir, inst := secretRepo(t, "A=env:SF_TEST_LATE_A")
	t.Setenv("SF_TEST_LATE_A", "late-a-value-1")
	calls := fakeOp(t, "op-value-must-not-be-read")
	mapSecret(t, root, "A", "op://Dev/acme/token")
	rootResolved, _ := identity.ResolvePath(root)
	branch, _ := currentBranch(root)
	project, instanceDir, err := checkoutInstance(rootResolved, branch, stateDir, filepath.Join(t.TempDir(), "legacy"))
	if err != nil || instanceDir != inst {
		t.Fatal(err)
	}
	exec := func(vals lifecycle.SecretValues) error {
		_, err := lifecycle.Execute(os.Stderr, os.Stderr, "up", rootResolved, branch, project, instanceDir, stateDir, "", lifecycle.Options{Secrets: vals, WaitTimeout: 10})
		return err
	}
	want := "secret A was not resolved by the secrets step; start again"
	for name, vals := range map[string]lifecycle.SecretValues{
		"empty set":      {},
		"different ref":  {"A": {Ref: "env:SF_TEST_LATE_A", Value: "late-a-value-1"}},
		"different, nil": {"B": {Ref: "env:X", Value: "x"}},
	} {
		if err := exec(vals); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("op was called: a reference was resolved late")
	}
	if err := exec(lifecycle.SecretValues{"A": {Ref: "op://Dev/acme/token", Value: "preflight-value-1"}}); err != nil {
		t.Errorf("the preflight's own set must run: %v", err)
	}
}

// A flag only takes effect where prepare records it. On an instance that is
// already prepared, start-env does not prepare again, so the manifest's
// reference is the one that runs and the preflight must resolve that one.
func TestStartEnvFlagOnPreparedInstanceDoesNotSplitPreflightFromUp(t *testing.T) {
	repo, _, base := siblingRepo(t)
	log := installFakeDocker(t)
	t.Setenv("SF_TEST_FLAG_B", "flag-b-value-1234")
	t.Setenv(sibTokenName, "manifest-a-value-1234")
	nw := newWorktree(t, repo, "feat-b")
	if out, e, code := runCLI(t, append(append([]string{}, base...), "start-env", nw)...); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	out, e, code := runCLI(t, append(append([]string{}, base...), "--secret", "tok=env:SF_TEST_FLAG_B", "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if note := "note: --secret tok ignored: " + project(t, base, nw) + " is already prepared; run `stackfork prepare --secret …` to change it."; strings.Count(out, note) != 1 {
		t.Errorf("want one %q in:\n%s", note, out)
	}
	env := dockerEnvLog(log)
	if !strings.Contains(env, "\ntok=manifest-a-value-1234") || strings.Contains(env, "\ntok=flag-b-value-1234") {
		t.Errorf("up must run with the manifest's reference:\n%s", env)
	}
}

// Round 2: a store reference lent by a sibling worktree is dropped: only the
// map holds those, so nothing reads the store for it and nothing fails on it.
func TestStartEnvDropsUnmappedStoreReferencesOfASibling(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	installFakeDocker(t)
	kc := fakeKeychain(t, nil)
	whereOut, _, _ := runCLI(t, append(append([]string{}, base...), "--root", sib, "where")...)
	var w struct{ State string }
	json.Unmarshal([]byte(whereOut), &w)
	mp := filepath.Join(w.State, "manifest.json")
	b, _ := os.ReadFile(mp)
	var m map[string]any
	json.Unmarshal(b, &m)
	m["secrets"].(map[string]any)["kc"] = "keychain:acme-sibling-only"
	m["secrets"].(map[string]any)["kept"] = "op://Dev/acme/kept"
	b, _ = json.Marshal(m)
	os.WriteFile(mp, b, 0o600)
	mapSecret(t, repo, "other", "op://Dev/acme/kept") // a mapped name reaches prepare through the map
	fakeOp(t, "op-value-1234567")
	nw := newWorktree(t, repo, "feat-b")
	out, e, code := runCLI(t, append(append([]string{}, base...), "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if strings.Contains(out+e, "acme-sibling-only") {
		t.Errorf("mentioned: %s %s", out, e)
	}
	if _, err := os.Stat(filepath.Join(kc, "args.rec")); err == nil {
		t.Error("the keychain was read")
	}
	refs := instanceManifestSecrets(t, base, nw)
	if _, ok := refs["kc"]; ok || refs["kept"] != "" || refs["other"] != "op://Dev/acme/kept" {
		t.Errorf("manifest %v", refs)
	}
}

// project is the instance project name of path.
func project(t *testing.T, base []string, path string) string {
	t.Helper()
	whereOut, _, _ := runCLI(t, append(append([]string{}, base...), "--root", path, "where")...)
	var w struct{ Project string }
	json.Unmarshal([]byte(whereOut), &w)
	return w.Project
}
