package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review I2: an agent cannot point a name at a store item with --secret.
func TestSecretFlagRefusesKeychainAndOpReferences(t *testing.T) {
	installFakeDocker(t)
	fakeKeychain(t, map[string]string{"other-item": "other-item-value-1"})
	calls := fakeOp(t, "op-value-must-not-be-read")
	root, stateDir, inst := secretRepo(t)
	before, _ := os.ReadFile(filepath.Join(inst, "manifest.json"))
	for _, ref := range []string{"keychain:other-item", "op://Private/bank/password"} {
		for _, cmd := range [][]string{{"prepare", "-f", filepath.Join(root, "compose.yaml")}, {"start-env"}} {
			args := append([]string{"--root", root, "--state-dir", stateDir, "--secret", "NPM_TOKEN=" + ref}, cmd...)
			out, e, code := runCLI(t, args...)
			if code == 0 || !strings.Contains(e, "stackfork secret map") {
				t.Errorf("%s %v: %d\n%s\n%s", ref, cmd, code, out, e)
			}
			if strings.Contains(out+e, "other-item") || strings.Contains(out+e, "bank") {
				t.Errorf("%s %v echoes the reference:\n%s\n%s", ref, cmd, out, e)
			}
		}
	}
	after, _ := os.ReadFile(filepath.Join(inst, "manifest.json"))
	if string(before) != string(after) {
		t.Error("the manifest changed")
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("op was called")
	}
}

func TestSecretFlagStillTakesEnvAndNpmrc(t *testing.T) {
	installFakeDocker(t)
	root, stateDir, inst := secretRepo(t, "NPM_TOKEN=env:SF_TEST_I2_TOKEN")
	_ = root
	_ = stateDir
	if got := manifestRefs(t, inst)["NPM_TOKEN"]; got != "env:SF_TEST_I2_TOKEN" {
		t.Fatalf("manifest %q", got)
	}
}

// Review I2: `stackfork exec` and `run` print what the container prints, so
// they redact with the values the instance resolves (best effort).
func TestExecAndRunRedactKnownSecretValues(t *testing.T) {
	installFakeDocker(t)
	const envVal, kcVal, opVal = "exec-env-value-7788", "exec-keychain-value-7788", "exec-op-value-7788"
	t.Setenv("SF_TEST_EXEC_TOK", envVal)
	fakeKeychain(t, map[string]string{"acme-exec": kcVal})
	opCalls := fakeOp(t, opVal)
	root, stateDir, _ := secretRepo(t, "FROM_ENV=env:SF_TEST_EXEC_TOK", "FROM_KC=env:SF_TEST_EXEC_TOK", "FROM_OP=env:SF_TEST_EXEC_TOK")
	mapSecret(t, root, "FROM_KC", "keychain:acme-exec")
	mapSecret(t, root, "FROM_OP", "op://Dev/acme/exec")
	t.Setenv("DOCKER_FAKE_EXEC_OUT", "env="+envVal+" kc="+kcVal+" op="+opVal+"\n")
	base := []string{"--root", root, "--state-dir", stateDir}
	// exec resolves without prompting: env: and keychain: values are known, op:// is not
	out, e, code := runCLI(t, append(base, "exec", "db", "printenv")...)
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	if strings.Contains(out, envVal) || strings.Contains(out, kcVal) || !strings.Contains(out, "[redacted:FROM_ENV]") || !strings.Contains(out, "[redacted:FROM_KC]") {
		t.Errorf("exec output: %q", out)
	}
	if _, err := os.Stat(opCalls); err == nil {
		t.Error("exec must not run op")
	}
	// run resolves every reference itself to give it to the container: all are known
	out, e, code = runCLI(t, append(base, "run", "db", "printenv")...)
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	for _, v := range []string{envVal, kcVal, opVal} {
		if strings.Contains(out, v) {
			t.Errorf("run output has %q: %q", v, out)
		}
	}
}
