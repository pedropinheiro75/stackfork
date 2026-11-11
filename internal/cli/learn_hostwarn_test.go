package cli

import (
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestLearnRecordStepWarnsOnLiteralPortsAndHostname(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	host := readManifestAt(t, stateDir, project).Hostname
	rec := func(args ...string) (string, int) {
		_, stderr, code := runCLI(t, append([]string{"--root", root, "--state-dir", stateDir, "learn", "record", "step"}, args...)...)
		return stderr, code
	}
	for _, args := range [][]string{
		{"--id", "w1", "--kind", "host", "--name", "w1", "--cmd", "vite", "--port", "13003"},
		{"--id", "w2", "--kind", "host", "--name", "w2", "--cmd", "vite --port 13003"},
		{"--id", "w3", "--kind", "host", "--name", "w3", "--cmd", "env UI_URL=http://" + host + ":1 vite"},
		{"--id", "w4", "--kind", "exec", "--on", "host", "--cmd", "prisma studio --port 15555"},
	} {
		if stderr, code := rec(args...); code != 0 || !strings.Contains(stderr, "{{port}}") {
			t.Errorf("%v: code %d stderr %q", args, code, stderr)
		}
	}
	if stderr, code := rec("--id", "ok", "--kind", "host", "--name", "ok", "--cmd", "vite --port {{port}}"); code != 0 || stderr != "" {
		t.Errorf("placeholders: code %d stderr %q", code, stderr)
	}
}

func TestLearnVerifyWarnsOnceOnLiteralHostValues(t *testing.T) {
	v := newVerifyEnv(t, closedPort(t), closedPort(t), &knowledge.Entry{None: true}, nil)
	k, err := knowledge.Load(v.path)
	if err != nil {
		t.Fatal(err)
	}
	k.Plan.Steps = append(k.Plan.Steps,
		knowledge.Step{ID: "app", Kind: knowledge.KindHost, Name: "app", Cmd: "vite --port 13003"},
		knowledge.Step{ID: "ok", Kind: knowledge.KindHost, Name: "ok", Cmd: "vite --port {{port}}"})
	top, _, _, manifest, err := checkoutManifest(v.root, v.flags[1], v.flags[3])
	if err != nil {
		t.Fatal(err)
	}
	recordPrepareRecipe(&k.Plan, top, manifest)
	if err := k.Save(v.path); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "plan", "--trust")...); code != 0 {
		t.Fatal(stderr)
	}
	out, stderr, code := v.verify(t)
	if code != 0 {
		t.Fatalf("verify: %s", stderr)
	}
	if n := strings.Count(out, "fixes port"); n != 1 || strings.Contains(out, "step ok") {
		t.Fatalf("warnings:\n%s", out)
	}
}
