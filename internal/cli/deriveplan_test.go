package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

const derivedAdapter = ".stackfork/dev.compose.yaml"

// deriveFix is one scratch repo (acme-web) with the state dirs and the fake
// docker's log: derive checks that nothing changed on disk and Docker was not called.
type deriveFix struct {
	repo, newBase, legacy, log string
}

func newDeriveFix(t *testing.T) deriveFix {
	t.Helper()
	root, state := reposEnv(t)
	repo := scratchRepo(t, filepath.Join(root, "acme-web"))
	return deriveFix{repo: repo, newBase: state, legacy: t.TempDir(), log: installFakeDocker(t)}
}

func (f deriveFix) base() []string {
	return []string{"--state-dir", f.newBase, "--legacy-state-dir", f.legacy}
}

func (f deriveFix) write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f deriveFix) know(t *testing.T, root string, k knowledge.Knowledge) {
	t.Helper()
	k.Schema = knowledge.CurrentSchema
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
}

func (f deriveFix) verified(t *testing.T, root string) {
	t.Helper()
	f.know(t, root, knowledge.Knowledge{Status: knowledge.StatusVerified})
}

// derive runs DerivePlan and fails when it wrote a file or called Docker.
func (f deriveFix) derive(t *testing.T, checkout, mode string) (lifecycle.DerivedPlan, error) {
	t.Helper()
	logBefore, _ := os.ReadFile(f.log)
	repoBefore, stateBefore := listTree(t, checkout), listTree(t, f.newBase)
	d, err := DerivePlan(DeriveOptions{Checkout: checkout, NewStateBase: f.newBase, LegacyStateBase: f.legacy, Mode: mode})
	if logAfter, _ := os.ReadFile(f.log); string(logAfter) != string(logBefore) {
		t.Errorf("deriving called docker:\n%s", logAfter)
	}
	if after := listTree(t, checkout); !reflect.DeepEqual(repoBefore, after) {
		t.Errorf("deriving changed the checkout:\nbefore %v\nafter  %v", repoBefore, after)
	}
	if after := listTree(t, f.newBase); !reflect.DeepEqual(stateBefore, after) {
		t.Errorf("deriving changed the state dir:\nbefore %v\nafter  %v", stateBefore, after)
	}
	return d, err
}

func stepIDs(d lifecycle.DerivedPlan) string {
	var l []string
	for _, s := range d.Steps {
		l = append(l, s.ID)
	}
	return strings.Join(l, ",")
}

func (f deriveFix) seedable(t *testing.T) {
	f.write(t, f.repo, "package.json", `{"scripts":{"db:migrate":"m","db:seed":"s"}}`)
}

type deriveCase struct {
	name  string
	setup func(t *testing.T, f deriveFix) string // returns the checkout to derive
	ids   string
	from  string
	src   map[string]string // step id -> substring of its source
}

var deriveCases = []deriveCase{
	{"adapter only", func(t *testing.T, f deriveFix) string {
		f.write(t, f.repo, derivedAdapter, "services: {}\n")
		f.verified(t, f.repo)
		return f.repo
	}, "prepare,up", "adapter", map[string]string{"prepare": "adapter " + derivedAdapter, "up": "implicit"}},
	{"adapter_files", func(t *testing.T, f deriveFix) string {
		f.write(t, f.repo, derivedAdapter, "services: {}\n")
		f.know(t, f.repo, knowledge.Knowledge{Status: knowledge.StatusVerified, AdapterFiles: []string{derivedAdapter}})
		return f.repo
	}, "prepare,up", "adapter_files", map[string]string{"prepare": "adapter_files in knowledge.json"}},
	{"already prepared", func(t *testing.T, f deriveFix) string {
		f.write(t, f.repo, "docker-compose.yml", "services:\n  app:\n    image: alpine:3.20\n")
		if _, e, code := runCLI(t, append(f.base(), "--root", f.repo, "prepare", "-f", "docker-compose.yml")...); code != 0 {
			t.Fatal(e)
		}
		return f.repo
	}, "prepare,up", "manifest", map[string]string{"prepare": "already prepared: reuses -f "}},
	{"auto-detected seed with migrate", func(t *testing.T, f deriveFix) string {
		f.write(t, f.repo, derivedAdapter, "services: {}\n")
		f.seedable(t)
		f.verified(t, f.repo)
		return f.repo
	}, "prepare,up,seed", "adapter", map[string]string{"seed": "auto-detected: npm run db:migrate && npm run db:seed; runs once"}},
	{"recorded no_seed", func(t *testing.T, f deriveFix) string {
		f.write(t, f.repo, derivedAdapter, "services: {}\n")
		f.seedable(t)
		f.know(t, f.repo, knowledge.Knowledge{Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, NoSeed: true}})
		return f.repo
	}, "prepare,up", "adapter", nil},
	{"sibling recipe", func(t *testing.T, f deriveFix) string {
		sib := filepath.Join(f.repo, ".worktrees", "feat-a")
		runGit(t, f.repo, "worktree", "add", "-q", "-b", "feat-a", sib)
		f.write(t, sib, "docker-compose.yml", "services:\n  app:\n    image: alpine:3.20\n")
		f.verified(t, sib)
		if _, e, code := runCLI(t, append(f.base(), "--root", sib, "prepare", "-f", "docker-compose.yml")...); code != 0 {
			t.Fatal(e)
		}
		nw := filepath.Join(f.repo, ".worktrees", "feat-b")
		runGit(t, f.repo, "worktree", "add", "-q", "-b", "feat-b", nw)
		return nw
	}, "prepare,up", "sibling", map[string]string{"prepare": "recipe of "}},
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func TestDerivePlan(t *testing.T) {
	for _, c := range deriveCases {
		t.Run(c.name, func(t *testing.T) {
			f := newDeriveFix(t)
			checkout := c.setup(t, f)
			d, err := f.derive(t, checkout, "")
			if err != nil {
				t.Fatal(err)
			}
			if d.Refusal != "" || stepIDs(d) != c.ids || d.PrepareFrom != c.from || d.Mode != knowledge.ModeContainer {
				t.Fatalf("%+v", d)
			}
			for _, s := range d.Steps {
				if s.State != lifecycle.StateDerived || s.Source == "" {
					t.Errorf("step %s: state %q source %q", s.ID, s.State, s.Source)
				}
				if want := c.src[s.ID]; !strings.Contains(s.Source, want) {
					t.Errorf("step %s source %q, want %q", s.ID, s.Source, want)
				}
			}
		})
	}
}

func TestDeriveSeedCommandHasTheMigratePrefix(t *testing.T) {
	f := newDeriveFix(t)
	f.write(t, f.repo, derivedAdapter, "services: {}\n")
	f.seedable(t)
	f.verified(t, f.repo)
	d, err := f.derive(t, f.repo, "")
	if err != nil || len(d.Steps) != 3 || d.Steps[2].Cmd != "npm run db:migrate && npm run db:seed" || d.Steps[2].Once != knowledge.OnceData {
		t.Fatalf("%+v %v", d, err)
	}
	if got := lifecycle.StepText(d.Steps[1].Step); got != "compose up" {
		t.Errorf("up text %q", got)
	}
	if got := lifecycle.StepText(d.Steps[0].Step); got != "prepare -f "+derivedAdapter {
		t.Errorf("prepare text %q", got)
	}
}

func TestDeriveRefusals(t *testing.T) {
	t.Run("not ready without a manifest", func(t *testing.T) {
		f := newDeriveFix(t)
		f.write(t, f.repo, derivedAdapter, "services: {}\n")
		d, err := f.derive(t, f.repo, "")
		if err != nil || !strings.Contains(d.Refusal, "stackfork only starts projects whose startup was learned") || len(d.Steps) != 0 {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("no recipe anywhere", func(t *testing.T) {
		f := newDeriveFix(t)
		f.verified(t, f.repo)
		d, err := f.derive(t, f.repo, "")
		if err != nil || !strings.Contains(d.Refusal, "no recorded prepare recipe") || len(d.Steps) != 0 {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("unsupported mode", func(t *testing.T) {
		// a plan without steps has no host step, so hybrid is the only way
		// to the plan check; an untrusted plan cannot reach it
		f := newDeriveFix(t)
		f.write(t, f.repo, derivedAdapter, "services: {}\n")
		f.verified(t, f.repo)
		d, err := f.derive(t, f.repo, knowledge.ModeHybrid)
		if err != nil || !strings.Contains(d.Refusal, `mode "hybrid" is not supported`) || len(d.Steps) != 0 {
			t.Fatalf("%+v %v", d, err)
		}
	})
}

func TestDerivePlanOnlyWithoutRecordedSteps(t *testing.T) {
	f := newDeriveFix(t)
	f.write(t, f.repo, derivedAdapter, "services: {}\n")
	f.know(t, f.repo, knowledge.Knowledge{Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded,
		Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}}}})
	if _, err := f.derive(t, f.repo, ""); !errors.Is(err, ErrRecordedPlan) {
		t.Errorf("recorded steps: %v", err)
	}
	f.write(t, f.repo, ".stackfork/knowledge.json", "{not json")
	if _, err := f.derive(t, f.repo, ""); err == nil || errors.Is(err, ErrRecordedPlan) {
		t.Errorf("a broken file is an error: %v", err)
	}
}

// A worktree prepared with fewer Compose files than the main checkout's last
// prepare shows the worktree-recipes warning.
func TestDeriveShowsTheRecipeGap(t *testing.T) {
	f := newDeriveFix(t)
	f.write(t, f.repo, "docker-compose.yml", "services:\n  app:\n    image: alpine:3.20\n")
	f.write(t, f.repo, derivedAdapter, "services: {}\n")
	if _, e, code := runCLI(t, append(f.base(), "--root", f.repo, "prepare", "-f", "docker-compose.yml", "-f", derivedAdapter)...); code != 0 {
		t.Fatal(e)
	}
	nw := filepath.Join(f.repo, ".worktrees", "feat")
	runGit(t, f.repo, "worktree", "add", "-q", "-b", "feat", nw)
	f.write(t, nw, derivedAdapter, "services: {}\n")
	f.verified(t, nw)
	d, err := f.derive(t, nw, "")
	if err != nil || !strings.Contains(d.Gap, "Missing: docker-compose.yml") || !strings.Contains(d.Gap, "reset --from-main") {
		t.Fatalf("%+v %v", d, err)
	}
	if main, err := f.derive(t, f.repo, ""); err != nil || main.Gap != "" {
		t.Errorf("the main checkout has no gap: %+v %v", main, err)
	}
}

// The derived steps are the ones a Start declares, in the same order.
func TestDerivedPlanMatchesRun(t *testing.T) {
	for _, c := range deriveCases {
		t.Run(c.name, func(t *testing.T) {
			f := newDeriveFix(t)
			checkout := c.setup(t, f)
			d, err := f.derive(t, checkout, "")
			if err != nil || d.Refusal != "" {
				t.Fatalf("%+v %v", d, err)
			}
			if out, e, code := runCLI(t, append(f.base(), "start-env", checkout)...); code != 0 {
				t.Fatalf("%d\n%s\n%s", code, out, e)
			}
			where, _, _ := runCLI(t, append(f.base(), "--root", checkout, "where")...)
			var w struct{ State string }
			json.Unmarshal([]byte(where), &w)
			r, err := runs.Get(w.State, 1)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range r.Steps {
				if s.Kind != "sync" && s.Kind != "secrets" { // the run's own steps, not the plan's
					got = append(got, s.ID)
				}
			}
			if strings.Join(got, ",") != stepIDs(d) {
				t.Errorf("run %v, derived %s", got, stepIDs(d))
			}
		})
	}
}
