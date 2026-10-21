package dashboard_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func recordEntry(t *testing.T, repo string, e *knowledge.Entry) {
	t.Helper()
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Entry: e}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
}

// D12: the Repos panel shows the entry line in each state, in the recipe
// block and in the plan bar.
func TestReposEntryLine(t *testing.T) {
	cmd := `data-copy="stackfork learn record entry --service S --port N"`
	for _, c := range []struct {
		name  string
		entry *knowledge.Entry
		want  []string
		not   []string
	}{
		{"recorded", &knowledge.Entry{Service: "nginx", Port: 80, Path: "/app"},
			[]string{`data-entry="recorded"`, `<span class="micro">App</span><span aria-hidden="true">&rarr;</span><code class="mono">nginx:80/app</code>`, "chip-ok", ">recorded<"}, []string{"entry guessed", "No web entry", cmd}},
		{"guessed", &knowledge.Entry{Service: "web", Port: 3000, Proposed: true},
			[]string{`data-entry="guessed"`, ">web:3000<", "chip-attention", "entry guessed", cmd}, []string{"No web entry", ">recorded<"}},
		{"none", &knowledge.Entry{None: true},
			[]string{`data-entry="none"`, "No web entry"}, []string{`<span class="micro">App</span>`, cmd, "entry guessed"}},
	} {
		for _, withPlan := range []bool{false, true} {
			e := newBrowserEnv(t)
			repo := e.readyRepo("svc")
			if withPlan {
				hybridPlan(t, repo, "app")
				file := filepath.Join(repo, ".stackfork", "knowledge.json")
				k, err := knowledge.Load(file)
				if err != nil {
					t.Fatal(err)
				}
				k.Entry = c.entry
				if err := k.Save(file); err != nil {
					t.Fatal(err)
				}
			} else {
				recordEntry(t, repo, c.entry)
			}
			e.get("/api/repos")
			frag := e.get("/fragments/repos").Body.String()
			for _, w := range c.want {
				if !strings.Contains(frag, w) {
					t.Errorf("%s plan=%v: missing %q in:\n%s", c.name, withPlan, w, frag)
				}
			}
			for _, n := range c.not {
				if strings.Contains(frag, n) {
					t.Errorf("%s plan=%v: has %q", c.name, withPlan, n)
				}
			}
		}
	}
}

func TestReposNoEntryLineWithoutKnowledge(t *testing.T) {
	e := newBrowserEnv(t)
	e.needsLearnRepo("svc")
	e.get("/api/repos")
	if frag := e.get("/fragments/repos").Body.String(); strings.Contains(frag, "data-entry=") {
		t.Fatalf("entry line without knowledge:\n%s", frag)
	}
}
