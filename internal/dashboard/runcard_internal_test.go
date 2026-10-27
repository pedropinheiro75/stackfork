package dashboard

import (
	"bytes"
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

func runInst(lr *inventory.LastRun, mutate func(*inventory.Instance)) inventory.Instance {
	i := inst("acme-web", "sf-acme-web-2ffa6841c75d", "feature-login", "stopped", false, time.Now())
	i.Root = "/src/acme-web/.worktrees/feature-login"
	i.LastRun = lr
	if mutate != nil {
		mutate(&i)
	}
	return i
}

func ago5() time.Time { return time.Now().Add(-5 * time.Minute) }

var runCardCases = []struct {
	name string
	inst inventory.Instance
	want []string // fragments of the card
	not  []string
}{
	{"host apps show their summary line", runInst(nil, func(i *inventory.Instance) {
		code := 1
		i.State = "partial"
		i.Services = []inventory.Service{
			{Name: "host:app", Label: "app", Kind: "host", Image: "host", State: "running", Meta: "pid 4242 \u00b7 312 MB \u00b7 3% CPU \u00b7 up 12m",
				Host: &inventory.HostInfo{PID: 4242, Listening: true}},
			{Name: "host:api", Label: "api", Kind: "host", Image: "host", State: "exited", Meta: "exited 1 at 17:12",
				Host: &inventory.HostInfo{ExitCode: &code}},
		}
	}), []string{`<span class="service-meta mono">pid 4242 · 312 MB · 3% CPU · up 12m</span>`, `<span class="service-meta mono">exited 1 at 17:12</span>`}, nil},
	{"host app that is up but not listening yet", runInst(nil, func(i *inventory.Instance) {
		i.State = "running"
		i.Services = []inventory.Service{{Name: "host:app", Label: "app", Kind: "host", Image: "host", State: "starting", Meta: "pid 4242 \u00b7 up 3s",
			Host: &inventory.HostInfo{PID: 4242}}}
	}), []string{`chip-busy`, "Starting…", `data-action="service-stop"`}, []string{`data-action="service-start"`, ">Stopped<"}},
	{"env overrides: chip, reset and an Environment item per service", runInst(nil, func(i *inventory.Instance) {
		i.State, i.EnvOverrides = "running", 2
		i.Services = []inventory.Service{
			{Name: "web", Kind: "web", Image: "nginx", State: "running"},
			{Name: "host:app", Label: "app", Kind: "host", Image: "host", State: "running", Host: &inventory.HostInfo{PID: 4242, Listening: true}},
		}
	}), []string{`<button type="button" class="chip chip-attention env-chip" data-env-list data-project="sf-acme-web-2ffa6841c75d"`, "2 env overrides</button>",
		`<button type="button" class="btn btn-ghost" data-env-reset data-project="sf-acme-web-2ffa6841c75d" data-count="2" data-key="sf-acme-web-2ffa6841c75d/envreset"`, ">Reset to project defaults<",
		`data-env-open data-project="sf-acme-web-2ffa6841c75d" data-service="web"`, `data-env-open data-project="sf-acme-web-2ffa6841c75d" data-service="host:app"`, ">Environment&hellip;<"}, nil},
	{"env item: a hostile service name is escaped", runInst(nil, func(i *inventory.Instance) {
		i.State = "running"
		i.Services = []inventory.Service{{Name: `<"'&`, Kind: "web", Image: "nginx", State: "running"}}
	}), []string{`data-env-open data-project="sf-acme-web-2ffa6841c75d" data-service="&lt;&#34;&#39;&amp;"`}, []string{`data-service="<`, `data-service="<"`}},
	{"one env override", runInst(nil, func(i *inventory.Instance) { i.EnvOverrides = 1 }),
		[]string{"1 env override</button>", "data-env-reset"}, []string{"1 env overrides"}},
	{"no env overrides, no chip and no reset", runInst(nil, func(i *inventory.Instance) {
		i.State = "running"
		i.Services = []inventory.Service{{Name: "web", Kind: "web", Image: "nginx", State: "stopped"}}
	}), []string{`data-env-open data-project="sf-acme-web-2ffa6841c75d" data-service="web"`}, []string{"env-chip", "data-env-reset", "env override"}},
	{"none", runInst(nil, nil), []string{`data-repo-action="reset" data-repo="/src/acme-web" data-path="/src/acme-web/.worktrees/feature-login"`, "Reset environment&hellip;",
		`data-key="sf-acme-web-2ffa6841c75d/menu/remove" data-run="stackfork down --instance sf-acme-web-2ffa6841c75d --volumes --forget"`,
		`data-copy="stackfork down --instance sf-acme-web-2ffa6841c75d --volumes"`},
		[]string{"callout", "Last run", "Starting", "Reset from main checkout"}},
	{"worktree with a main instance", runInst(nil, func(i *inventory.Instance) { i.FromMain = true }),
		[]string{`data-repo-action="reset" data-from-main="1" data-repo="/src/acme-web" data-path="/src/acme-web/.worktrees/feature-login"`, "Reset from main checkout&hellip;", "Reset environment&hellip;"},
		[]string{"callout", "sf-acme-web-2ffa6841c75d/warnings/resetmain"}},
	{"recipe gap", runInst(nil, func(i *inventory.Instance) {
		i.FromMain, i.RecipeGap = true, true
		i.Warnings = []string{"warning: this worktree prepares with 1 Compose file (a.yaml); the main checkout's last prepare used 2 (b.yaml, a.yaml). Missing: b.yaml. To reuse the main checkout's recipe: stackfork --root /src/acme-web/.worktrees/feature-login reset --from-main"}
	}), []string{`data-callout="warnings"`, "Missing: b.yaml", `<div class="callout-actions"><button type="button" class="btn" data-repo-action="reset" data-from-main="1" data-repo="/src/acme-web" data-path="/src/acme-web/.worktrees/feature-login" data-key="sf-acme-web-2ffa6841c75d/warnings/resetmain"`, ">Reset from main checkout&hellip;<"},
		[]string{"Run command", "Copy command"}},
	{"warning without the action", runInst(nil, func(i *inventory.Instance) { i.Warnings = []string{"checkout removed"} }),
		[]string{`data-callout="warnings"`}, []string{"warnings/resetmain", "Reset from main checkout"}},
	{"failed reset from main", runInst(&inventory.LastRun{ID: 14, Command: "reset", Trigger: "dashboard", Outcome: "failed", FailedStep: "prepare", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --root /src/acme-web/.worktrees/feature-login reset --yes --from-main", Tail: "boom"}, nil),
		[]string{`Reset failed at step <code class="mono">prepare</code>`, `data-repo-action="reset" data-from-main="1" data-repo="/src/acme-web" data-path="/src/acme-web/.worktrees/feature-login" data-key="sf-acme-web-2ffa6841c75d/run/retry" title="Reset again"`},
		[]string{`data-run="stackfork --root`}},
	{"resetting", runInst(&inventory.LastRun{ID: 12, Command: "reset", Trigger: "dashboard", Outcome: "running", Step: "down", StepN: 3, StepTotal: 8, StartedAt: ago5()}, nil),
		[]string{`title="step 3 of 8: down"`, "Resetting…"}, []string{"Starting…", "callout"}},
	{"failed reset", runInst(&inventory.LastRun{ID: 13, Command: "reset", Trigger: "dashboard", Outcome: "failed", FailedStep: "up", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --root /src/acme-web/.worktrees/feature-login reset --yes", Tail: "no space left"}, nil),
		[]string{`Reset failed at step <code class="mono">up</code>`, `data-repo-action="reset" data-repo="/src/acme-web" data-path="/src/acme-web/.worktrees/feature-login" data-key="sf-acme-web-2ffa6841c75d/run/retry" title="Reset again"`, "no space left"},
		[]string{"Start failed", `data-repo-action="start"`, `data-run="stackfork --root`}},
	{"failed env change", runInst(&inventory.LastRun{ID: 14, Command: "env", Trigger: "dashboard", Outcome: "failed", FailedStep: "restart-api", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --instance sf-acme-web-2ffa6841c75d host restart api", Tail: "Error: boom"}, nil),
		[]string{`Env change failed at step <code class="mono">restart-api</code>`, `data-dismiss-run="14"`, ">Dismiss<"}, []string{"Start failed"}},
	{"running", runInst(&inventory.LastRun{ID: 3, Command: "start-env", Trigger: "cli", Outcome: "running", Step: "seed", StepN: 3, StepTotal: 6, StartedAt: ago5()}, nil),
		[]string{`title="step 3 of 6: seed"`, "Starting…"}, []string{">Stopped<", "callout"}},
	{"failed start-env", runInst(&inventory.LastRun{ID: 4, Command: "start-env", Trigger: "dashboard", Outcome: "failed", FailedStep: "app", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --root /src/acme-web/.worktrees/feature-login start-env", Tail: "line 1\nboom <b>"}, nil),
		[]string{`class="callout callout-failed" data-run-outcome="failed"`, `Start failed at step <code class="mono">app</code>`, `data-repo-action="start"`,
			`data-repo="/src/acme-web"`, `data-path="/src/acme-web/.worktrees/feature-login"`, "line 1\nboom &lt;b&gt;", `data-run-id="4" data-step="app"`, ">View log<"}, []string{"data-seed", "Last run"}},
	{"failed up", runInst(&inventory.LastRun{ID: 5, Command: "up", Trigger: "cli", Outcome: "failed", FailedStep: "up", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --instance sf-acme-web-2ffa6841c75d up"}, nil),
		[]string{`data-run="stackfork --instance sf-acme-web-2ffa6841c75d up"`, ">Retry<"}, []string{`data-repo-action="start"`, "mono-log"}},
	{"failed without a step", runInst(&inventory.LastRun{ID: 6, Command: "up", Trigger: "cli", Outcome: "failed", StartedAt: ago5(), EndedAt: ago5()}, nil),
		[]string{">Start failed</p>"}, []string{">Retry<", ">View log<", "<code"}},
	{"interrupted", runInst(&inventory.LastRun{ID: 7, Command: "start-env", Trigger: "cli", Outcome: "interrupted", FailedStep: "up", StartedAt: ago5(),
		Retry: "stackfork --root /src/acme-web/.worktrees/feature-login start-env"}, nil),
		[]string{`class="callout callout-attention" data-run-outcome="interrupted"`, "Start interrupted at step", ">Retry<"}, []string{"callout-failed"}},
	{"succeeded", runInst(&inventory.LastRun{ID: 8, Command: "up", Trigger: "cli", Outcome: "succeeded", StartedAt: ago5(), EndedAt: ago5().Add(41 * time.Second)}, nil),
		[]string{"Last run", "succeeded · 41 s · 5 min ago", `data-run-id="8" data-step=""`}, []string{"callout"}},
	{"seed failure reported once", runInst(&inventory.LastRun{ID: 9, Command: "start-env", Trigger: "cli", Outcome: "failed", FailedStep: "seed", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --instance sf-acme-web-2ffa6841c75d seed", Tail: "duplicate key"}, func(i *inventory.Instance) {
		i.SeedCommand, i.SeedForce, i.SeedDone = "stackfork --instance P seed", "stackfork --instance P seed --force", "stackfork --instance P seed --mark-done"
		i.SeedError, i.SeedRun, i.SeedStep = "duplicate key", 9, "seed"
	}), []string{`data-seed="failed"`, `data-run-log data-project="sf-acme-web-2ffa6841c75d" data-run-id="9" data-step="seed"`, ">View log<"}, []string{`data-run-outcome="failed"`, "Start failed"}},
	{"interrupted at the seed shows both", runInst(&inventory.LastRun{ID: 11, Command: "start-env", Trigger: "cli", Outcome: "interrupted", FailedStep: "seed", StartedAt: ago5(),
		Retry: "stackfork --root /src/acme-web/.worktrees/feature-login start-env"}, func(i *inventory.Instance) {
		i.SeedCommand, i.SeedDone, i.SeedForce = "stackfork --instance P seed", "d", "f"
		i.SeedRun, i.SeedStep = 11, "seed"
	}), []string{`data-seed="failed"`, `data-run-outcome="interrupted"`, "Start interrupted at step"}, nil},
	{"live run in progress", runInst(&inventory.LastRun{ID: 30, Command: "live", Trigger: "live", Outcome: "running", Step: "rebuild", StepN: 2, StepTotal: 4, StartedAt: ago5()}, nil),
		[]string{`title="step 2 of 4: rebuild"`, "Starting…"}, []string{"callout", "Resetting"}},
	{"live run failed", runInst(&inventory.LastRun{ID: 31, Command: "live", Trigger: "live", Outcome: "failed", FailedStep: "migrate", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --instance sf-acme-web-2ffa6841c75d up", Tail: "boom"}, nil),
		[]string{`class="callout callout-failed" data-run-outcome="failed"`, `Live run failed at step <code class="mono">migrate</code>`, `data-live-op="resume" data-repo="/src/acme-web" data-key="sf-acme-web-2ffa6841c75d/run/retry" title="Resume live mode">Resume<`},
		[]string{">Retry<", `data-run="stackfork --instance`, `data-repo-action="start"`}},
	{"live run succeeded", runInst(&inventory.LastRun{ID: 32, Command: "live", Trigger: "live", Outcome: "succeeded", StartedAt: ago5(), EndedAt: ago5().Add(41 * time.Second)}, nil),
		[]string{"Last run", "live · succeeded · 41 s · 5 min ago"}, []string{"callout"}},
	{"cleanup suggest", runInst(nil, func(i *inventory.Instance) {
		i.Cleanup = &inventory.CleanupNote{Repo: "/src/acme-web", Path: i.Root, Branch: "feature-login", Title: "PR merged — clean up?", PR: 42, URL: "https://example.com/acme/web/pull/42", Ask: "suggest"}
	}), []string{`data-cleanup="suggest"`, "PR merged — clean up?", `<a href="https://example.com/acme/web/pull/42" target="_blank" rel="noopener">#42</a>`,
		`data-live-op="clean" data-repo="/src/acme-web" data-path="/src/acme-web/.worktrees/feature-login"`, ">Clean up<", `data-live-op="keep"`, ">Keep<"}, []string{"Clean up now"}},
	{"cleanup auto", runInst(nil, func(i *inventory.Instance) {
		i.Cleanup = &inventory.CleanupNote{Repo: "/src/acme-web", Path: i.Root, Branch: "feature-login", Title: "Cleanup in 23 h", Ask: "auto"}
	}), []string{`data-cleanup="auto"`, "Cleanup in 23 h", ">Clean up now<", ">Keep<"}, []string{"<a "}},
	{"cleanup warning", runInst(nil, func(i *inventory.Instance) {
		i.Cleanup = &inventory.CleanupNote{Repo: "/src/acme-web", Path: i.Root, Branch: "feature<login", Title: "PR merged, but this worktree has uncommitted changes"}
	}), []string{`data-cleanup="warning"`, "uncommitted changes", "feature&lt;login"}, []string{"data-live-op", "callout-actions"}},
	{"seed callout of an older run", runInst(&inventory.LastRun{ID: 10, Command: "up", Trigger: "cli", Outcome: "failed", FailedStep: "up", StartedAt: ago5(), EndedAt: ago5(),
		Retry: "stackfork --instance sf-acme-web-2ffa6841c75d up"}, func(i *inventory.Instance) {
		i.SeedCommand, i.SeedDone, i.SeedForce = "stackfork --instance P seed", "d", "f"
		i.SeedRun, i.SeedStep = 8, "seed"
	}), []string{`data-seed="failed"`, `data-run-outcome="failed"`, "Start failed at step"}, nil},
}

func renderVersion(t *testing.T, i inventory.Instance) string {
	t.Helper()
	ps := buildProjects([]inventory.Instance{i})
	var buf bytes.Buffer
	if err := indexTemplate.ExecuteTemplate(&buf, "version", ps[0].Versions[0]); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// Each outcome shows the right card pieces, and app.js renders the same card.
func TestVersionCardLastRunTemplateAndScriptAgree(t *testing.T) {
	var insts []inventory.Instance
	for _, c := range runCardCases {
		insts = append(insts, c.inst)
		got := renderVersion(t, c.inst)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: card lacks %q\n%s", c.name, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: card has %q\n%s", c.name, n, got)
			}
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json")
	b, _ := json.Marshal(insts)
	os.WriteFile(in, b, 0o600)
	cmd := exec.Command(node, abs(t, "testdata/appjs_harness.js"), abs(t, "static/app.js"), abs(t, "testdata/card_parity.case.js"))
	cmd.Env = append(os.Environ(), "SF_PARITY_IN="+in, "SF_PARITY_OUT="+out)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, o)
	}
	var js []string
	raw, _ := os.ReadFile(out)
	if err := json.Unmarshal(raw, &js); err != nil || len(js) != len(insts) {
		t.Fatalf("%v %s", err, raw)
	}
	for i, c := range runCardCases {
		a, b := normalizeCard(renderVersion(t, c.inst)), normalizeCard(js[i])
		if a != b {
			t.Errorf("%s: template and app.js differ\ntemplate: %s\napp.js:   %s", c.name, a, b)
		}
	}
}

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// normalizeCard drops what the two renderers spell differently without
// meaning anything different: entities, and whitespace between tags.
func normalizeCard(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\n<", "<")
	return strings.TrimSpace(s)
}

func TestServiceChipHostNotAnswering(t *testing.T) {
	c := serviceChip(inventory.Service{State: "running", Host: &inventory.HostInfo{Port: 3000}})
	if c.Class != "attention" || c.Label != "Running · not answering on :3000" {
		t.Errorf("%+v", c)
	}
	if c := serviceChip(inventory.Service{State: "running", Host: &inventory.HostInfo{Port: 3000, Listening: true}}); c.Label != "Running" {
		t.Errorf("%+v", c)
	}
	if c := serviceChip(inventory.Service{State: "finished", Meta: "exit 0 at 18:10"}); c.Class != "off" || c.Label != "Finished · exit 0 at 18:10" {
		t.Errorf("%+v", c)
	}
}
