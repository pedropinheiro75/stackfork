package lifecycle

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// An exec step with no service that can run it fails naming the stack's
// services and the command that records the right one.
func TestExecStepWithoutServiceSaysHowToFixIt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"web":{"build":"."},"db":{},"cache":{}}}`), 0o600)
	r := PlanRunner{InstanceDir: dir}
	_, err := r.exec(io.Discard, knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: "bun run db:migrate"})
	for _, want := range []string{"step migrate cannot run", "names no service", "this stack has: cache, db, web", "stackfork learn record step --id migrate --kind exec --service SERVICE"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	_, err = r.exec(io.Discard, knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Service: "app", Cmd: "x"})
	if err == nil || !strings.Contains(err.Error(), "its service app is not in this stack") {
		t.Errorf("got %v", err)
	}
}

func hostHintStack(t *testing.T, services string) PlanRunner {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":`+services+`}`), 0o600)
	return PlanRunner{InstanceDir: dir}
}

const hostRecord = `stackfork learn record step --id migrate --kind exec --on host --cmd "bun run db:migrate" --once schema`

// D8: when no service of the stack can run an app command, the error ends with
// the command that records the step on the host.
func TestMissingServiceSuggestsTheHostWhenNoServiceRunsTheApp(t *testing.T) {
	step := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: "bun run db:migrate", Once: knowledge.OnceSchema}
	for name, services := range map[string]string{
		"database and mail catcher": `{"postgres":{"image":"postgres:17","ports":[{"target":5432}]},"mailpit":{"image":"axllent/mailpit","ports":[{"target":8025}],"labels":{"io.stackfork.http":"8025"}}}`,
		"database and cache":        `{"db":{"image":"mysql:8"},"cache":{"image":"redis:7"}}`,
	} {
		_, err := hostHintStack(t, services).exec(io.Discard, step)
		for _, want := range []string{"no service of this stack runs the app; to run it on the host with this instance's environment:", hostRecord} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: missing %q in %v", name, want, err)
			}
		}
		if err != nil && strings.Contains(err.Error(), "--service SERVICE") {
			t.Errorf("%s: must not suggest --service: %v", name, err)
		}
	}
	// a single-service postgres stack: a step that names a service it lacks
	named := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: "bun run db:migrate", Once: knowledge.OnceSchema, Service: "web"}
	if _, err := hostHintStack(t, `{"postgres":{"image":"postgres:17"}}`).exec(io.Discard, named); err == nil || !strings.Contains(err.Error(), hostRecord) {
		t.Errorf("single postgres: %v", err)
	}
	// the suggestion repeats the step's cmd and once; once is left out when unset
	step.Once = ""
	if _, err := hostHintStack(t, `{"db":{"image":"mysql:8"},"cache":{"image":"redis:7"}}`).exec(io.Discard, step); err == nil || !strings.HasSuffix(err.Error(), `--cmd "bun run db:migrate"`) {
		t.Errorf("without once: %v", err)
	}
}

// A command with shell syntax cannot be recorded on the host (SplitArgv would
// refuse it): the hint says it needs a shell-free form instead.
func TestHostHintForAShellCommandSaysItNeedsAShellFreeForm(t *testing.T) {
	stack := `{"postgres":{"image":"postgres:17"},"cache":{"image":"redis:7"}}`
	for _, cmd := range []string{"bun run a && bun run b", "bun run a | tee x", "bun run a; bun run b", "bun run a > out.txt"} {
		step := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: cmd}
		_, err := hostHintStack(t, stack).exec(io.Discard, step)
		if err == nil || strings.Contains(err.Error(), "--on host") || !strings.Contains(err.Error(), "shell-free form") {
			t.Errorf("%q: %v", cmd, err)
		}
	}
}

func TestMissingServiceStillSuggestsServiceWhenAnAppServiceExists(t *testing.T) {
	step := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: "bun run db:migrate", Service: "api"}
	for name, r := range map[string]PlanRunner{
		"built":       hostHintStack(t, `{"web":{"build":"."},"db":{"image":"postgres:17"}}`),
		"app service": hostHintStack(t, `{"web":{"image":"acme/web"},"db":{"image":"postgres:17"}}`),
		"primary":     hostHintStack(t, `{"web":{"image":"acme/web","ports":[{"target":3000}]},"db":{"image":"postgres:17"}}`),
	} {
		if name == "app service" {
			r.Plan.AppService = "web"
		}
		_, err := r.exec(io.Discard, step)
		if err == nil || !strings.Contains(err.Error(), "--service SERVICE") || strings.Contains(err.Error(), "--on host") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The host branch of exec checks trust itself, whatever ran before it.
func TestPlanRunnerExecRefusesAnUntrustedHostStep(t *testing.T) {
	step := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Cmd: "bun run db:migrate", Once: knowledge.OnceSchema}
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{step}}
	var calls int
	old := HostExec
	HostExec = func(io.Writer, string, string, string, string, string, knowledge.Step) (int, error) { calls++; return 0, nil }
	t.Cleanup(func() { HostExec = old })
	r := PlanRunner{NewBase: t.TempDir(), Main: t.TempDir(), Plan: plan}
	if _, err := r.exec(io.Discard, step); err == nil || !strings.Contains(err.Error(), "not trusted on this machine") || calls != 0 {
		t.Fatalf("untrusted: err %v, host calls %d", err, calls)
	}
	if _, err := TrustHere(r.NewBase, r.Main, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := r.exec(io.Discard, step); err != nil || calls != 1 {
		t.Fatalf("trusted: err %v, host calls %d", err, calls)
	}
}
