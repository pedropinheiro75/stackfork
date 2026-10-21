package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
)

const secretCanary = "secret-cmd-canary-value-5"

// secretCmdFixture is a repo with one keychain secret that resolves, one
// 1Password secret and one declared name with no reference.
func secretCmdFixture(t *testing.T) (repo string, base []string, opCalls string) {
	t.Helper()
	root, state := reposEnv(t)
	repo = adaptedRepo(t, root)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Secrets: []knowledge.SecretDecl{{Name: "NPM_TOKEN", Hint: "GitHub Packages read token"}, {Name: "SENTRY_DSN", Hint: "ask the lead"}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	fakeKeychain(t, map[string]string{"acme-npm-token": secretCanary})
	opCalls = fakeOp(t, secretCanary)
	mapSecret(t, repo, "NPM_TOKEN", "keychain:acme-npm-token")
	mapSecret(t, repo, "STRIPE_TEST_KEY", "op://Dev/acme-stripe/test-key")
	return repo, []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}, opCalls
}

func TestSecretLsNeverPromptsAndPrintsNoValue(t *testing.T) {
	repo, base, opCalls := secretCmdFixture(t)
	out, e, code := runCLI(t, append(base, "secret", "ls", repo)...)
	if code != 0 || e != "" {
		t.Fatalf("%d %s", code, e)
	}
	assertGolden(t, "secret-ls.txt", out)
	if b, _ := os.ReadFile(opCalls); len(b) != 0 {
		t.Fatalf("ls ran op:\n%s", b)
	}
	jsonOut, _, code := runCLI(t, append(base, "secret", "ls", repo, "--json")...)
	if code != 0 {
		t.Fatal(code)
	}
	assertGolden(t, "secret-ls.json", jsonOut)
	if strings.Contains(out+jsonOut, secretCanary) {
		t.Fatal("a value reached the output")
	}
}

func TestSecretCheckReadsEveryValueAndExitsOneOnAnyFailure(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	out, e, code := runCLI(t, append(base, "secret", "check", repo)...)
	if code != 1 || e != "" {
		t.Fatalf("%d %s", code, e)
	}
	assertGolden(t, "secret-check.txt", out)
	jsonOut, _, _ := runCLI(t, append(base, "secret", "check", repo, "--json")...)
	assertGolden(t, "secret-check.json", jsonOut)
	if strings.Contains(out+jsonOut+e, secretCanary) {
		t.Fatal("a value reached the output")
	}
}

func TestSecretCheckAllResolvedExitsZero(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	runCLI(t, append(base, "secret", "map", repo, "SENTRY_DSN=keychain:acme-npm-token")...)
	if out, e, code := runCLI(t, append(base, "secret", "check", repo)...); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
}

func TestSecretCheckLockedKeychain(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	kc := fakeKeychain(t, map[string]string{"acme-npm-token": secretCanary})
	os.WriteFile(filepath.Join(kc, "locked"), nil, 0o600)
	out, e, code := runCLI(t, append(base, "secret", "check", repo, "--json")...)
	if code != 1 || !strings.Contains(out, `"status": "locked"`) || strings.Contains(out+e, secretCanary) {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
}

func TestSecretListUsesTheCheckoutByDefault(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	out, _, code := runCLI(t, append(base, "--root", repo, "secret", "ls", "--json")...)
	if code != 0 || !strings.Contains(out, `"name": "NPM_TOKEN"`) {
		t.Fatalf("%d %s", code, out)
	}
}

// --- set ------------------------------------------------------------------

func withStdin(t *testing.T, input string) {
	t.Helper()
	old := secretStdin
	secretStdin = strings.NewReader(input)
	t.Cleanup(func() { secretStdin = old })
}

func TestSecretSetFromStdinStoresWithoutAValueInArgv(t *testing.T) {
	_, base, _ := secretCmdFixture(t)
	kc := fakeKeychain(t, nil)
	withStdin(t, secretCanary+"\n")
	out, e, code := runCLI(t, append(base, "secret", "set", "acme-new")...)
	if code != 0 || strings.Contains(out+e, secretCanary) {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if !strings.HasPrefix(out, "stored keychain:acme-new\n") || !strings.Contains(out, "stackfork secret map") {
		t.Fatalf("output %q", out)
	}
	stdin, _ := os.ReadFile(filepath.Join(kc, "stdin.rec"))
	args, _ := os.ReadFile(filepath.Join(kc, "args.rec"))
	if !strings.Contains(string(stdin), "-w '"+secretCanary+"'") || strings.Contains(string(args), secretCanary) {
		t.Fatalf("stdin %q args %q", stdin, args)
	}
	if v, st, _ := secretstore.Resolve("keychain:acme-new"); st != secretstore.Resolves || v != secretCanary {
		t.Fatalf("the item was not stored: %s", st)
	}
}

func TestSecretSetOmitsTheMapHintWhenAMapEntryUsesTheItem(t *testing.T) {
	_, base, _ := secretCmdFixture(t)
	withStdin(t, "rotated-value-1\n")
	out, _, code := runCLI(t, append(base, "secret", "set", "acme-npm-token")...)
	if code != 0 || out != "stored keychain:acme-npm-token\n" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestSecretSetRefusals(t *testing.T) {
	_, base, _ := secretCmdFixture(t)
	kc := fakeKeychain(t, nil)
	for name, input := range map[string]string{
		"empty":         "",
		"only newline":  "\n",
		"inner newline": "line1\nline2\n",
		"NUL":           "a\x00b\n",
		"64 KiB + 1":    strings.Repeat("x", secretstore.MaxValueSize+1) + "\n",
	} {
		withStdin(t, input)
		out, e, code := runCLI(t, append(base, "secret", "set", "acme-bad")...)
		if code == 0 || strings.Contains(out+e, "line1") {
			t.Errorf("%s: code %d\n%s\n%s", name, code, out, e)
		}
	}
	if _, err := os.Stat(filepath.Join(kc, "items", "acme-bad")); err == nil {
		t.Fatal("a refused value was stored")
	}
	withStdin(t, strings.Repeat("x", secretstore.MaxValueSize)+"\n")
	if _, e, code := runCLI(t, append(base, "secret", "set", "acme-big")...); code != 0 {
		t.Fatalf("exactly 64 KiB must be accepted: %s", e)
	}
}

func TestSecretSetTakesNoValueArgument(t *testing.T) {
	_, base, _ := secretCmdFixture(t)
	withStdin(t, "x\n")
	_, e, code := runCLI(t, append(base, "secret", "set", "acme-x", "typed-on-the-command-line")...)
	if code != 1 || strings.Contains(e, "typed-on-the-command-line") {
		t.Fatalf("%d %s", code, e)
	}
	if _, e, code := runCLI(t, append(base, "secret", "set", "bad item")...); code != 1 || strings.Contains(e, "bad item") {
		t.Fatalf("%d %s", code, e)
	}
	if _, _, code := runCLI(t, append(base, "secret", "set", "acme-x", "--store", "vault")...); code != 1 {
		t.Fatalf("%d", code)
	}
}

// The terminal path, with the echo switch and the terminal check replaced
// (the standard library has no pseudo-terminal).
func TestSecretSetPromptsTwiceWithEchoOff(t *testing.T) {
	_, base, _ := secretCmdFixture(t)
	kc := fakeKeychain(t, nil)
	var echo []bool
	oldEcho, oldTTY := secretSetEcho, secretIsTerminal
	secretSetEcho = func(on bool) error { echo = append(echo, on); return nil }
	secretIsTerminal = func(io.Reader) bool { return true }
	defer func() { secretSetEcho, secretIsTerminal = oldEcho, oldTTY }()

	withStdin(t, secretCanary+"\n"+secretCanary+"\n")
	out, e, code := runCLI(t, append(base, "secret", "set", "acme-prompt")...)
	if code != 0 || strings.Contains(out+e, secretCanary) || !strings.Contains(e, "hidden") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if len(echo) != 2 || echo[0] || !echo[1] {
		t.Fatalf("echo must go off, then on again: %v", echo)
	}
	if _, err := os.Stat(filepath.Join(kc, "items", "acme-prompt")); err != nil {
		t.Fatal("not stored")
	}

	echo = nil
	withStdin(t, "first-entry\nsecond-entry\n")
	_, e, code = runCLI(t, append(base, "secret", "set", "acme-mismatch")...)
	if code == 0 || !strings.Contains(e, "differ") || strings.Contains(e, "first-entry") || len(echo) != 2 || !echo[1] {
		t.Fatalf("%d %s %v", code, e, echo)
	}
	if _, err := os.Stat(filepath.Join(kc, "items", "acme-mismatch")); err == nil {
		t.Fatal("mismatched entries were stored")
	}

	withStdin(t, "")
	if _, _, code := runCLI(t, append(base, "secret", "set", "acme-eof")...); code == 0 {
		t.Fatal("no entry must fail")
	}
	secretSetEcho = func(bool) error { return errors.New("no tty") }
	withStdin(t, "x\nx\n")
	if _, _, code := runCLI(t, append(base, "secret", "set", "acme-notty")...); code == 0 {
		t.Fatal("a failing stty must fail")
	}
}

// --- map / unmap ----------------------------------------------------------

func TestSecretMapPrintsStatusWithoutAPrompt(t *testing.T) {
	repo, base, opCalls := secretCmdFixture(t)
	out, e, code := runCLI(t, append(base, "secret", "map", repo, "OTHER_KEY=op://Dev/acme-other/key")...)
	if code != 0 || !strings.Contains(out, "mapped OTHER_KEY to op://Dev/acme-other/key") || !strings.Contains(out, "status: not checked") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if b, _ := os.ReadFile(opCalls); len(b) != 0 {
		t.Fatalf("map ran op:\n%s", b)
	}
	out, _, _ = runCLI(t, append(base, "secret", "map", repo, "MISSING=keychain:acme-none")...)
	if !strings.Contains(out, "status: missing") || !strings.Contains(out, "stackfork secret set acme-none") {
		t.Fatalf("%s", out)
	}
	if lsOut, _, _ := runCLI(t, append(base, "secret", "ls", repo, "--json")...); !strings.Contains(lsOut, "OTHER_KEY") {
		t.Fatal("map did not persist")
	}
}

func TestSecretMapRefusesAPastedValueWithoutEchoingIt(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	out, e, code := runCLI(t, append(base, "secret", "map", repo, "NPM_TOKEN=ghp_pastedTokenValue123")...)
	if code == 0 || strings.Contains(out+e, "ghp_pastedTokenValue123") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if _, _, code := runCLI(t, append(base, "secret", "map", repo)...); code != 1 {
		t.Fatalf("usage %d", code)
	}
}

func TestSecretUnmap(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	if out, e, code := runCLI(t, append(base, "secret", "unmap", repo, "STRIPE_TEST_KEY")...); code != 0 || !strings.Contains(out, "unmapped STRIPE_TEST_KEY") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if lsOut, _, _ := runCLI(t, append(base, "secret", "ls", repo, "--json")...); strings.Contains(lsOut, "STRIPE_TEST_KEY") {
		t.Fatal("still mapped")
	}
	if _, e, code := runCLI(t, append(base, "secret", "unmap", repo, "STRIPE_TEST_KEY")...); code != 1 || !strings.Contains(e, "not mapped") {
		t.Fatalf("%d %s", code, e)
	}
}

func TestSecretMapAcceptsAWorktreePath(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	sib := filepath.Join(repo, ".worktrees", "feat-a")
	if _, e, code := runCLI(t, append(base, "worktree", "add", repo, "feat-a", "--from", "main")...); code != 0 {
		t.Fatal(e)
	}
	if out, _, code := runCLI(t, append(base, "secret", "map", sib, "WT=env:SF_TEST_WT")...); code != 0 || !strings.Contains(out, "for "+repo) {
		t.Fatalf("%d %s (want the main repo %s)", code, out, repo)
	}
}

// A store tool that prints a value and fails must not get the value out.
func TestSecretCheckDropsToolOutputOfAFailingTool(t *testing.T) {
	repo, base, _ := secretCmdFixture(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	tool := filepath.Join(dir, "op")
	os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s' '"+secretCanary+"'\necho 'you are not signed in' >&2\nexit 1\n"), 0o755)
	secretstore.Default.OpPaths = []string{tool} // fakeOp's cleanup restores the backend
	out, e, code := runCLI(t, append(base, "secret", "check", repo)...)
	if code != 1 || !strings.Contains(out, "locked") || strings.Contains(out+e, secretCanary) {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
}
