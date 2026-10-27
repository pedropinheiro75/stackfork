package cli

import (
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestLearnRecordReload(t *testing.T) {
	root, path := seedKnowledge(t)
	k, _ := knowledge.Load(path)
	k.Reload = []knowledge.Reload{{Service: "web", Mode: "rebuild", Proposed: true, Evidence: "Dockerfile:2"}}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := learnRec(t, root, "reload", "web", "sometimes"); code == 0 || !strings.Contains(stderr, "hot, restart or rebuild") {
		t.Fatalf("bad mode: %d %s", code, stderr)
	}
	if out, stderr, code := learnRec(t, root, "reload", "web", "hot", "--note", "vite in the container"); code != 0 || !strings.Contains(out, `"recorded": "reload"`) {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	k, _ = knowledge.Load(path)
	if r := k.ReloadFor("web"); r == nil || r.Mode != "hot" || r.Proposed || r.Note != "vite in the container" || len(k.Reload) != 1 {
		t.Fatalf("%+v", k.Reload)
	}
}

func TestReloadWarning(t *testing.T) {
	k := &knowledge.Knowledge{Entry: &knowledge.Entry{Service: "web", Port: 80}}
	if w := reloadWarning(k); len(w) != 1 || !strings.Contains(w[0], "learn record reload web") {
		t.Fatalf("unknown: %v", w)
	}
	k.Reload = []knowledge.Reload{{Service: "web", Mode: "hot", Proposed: true}}
	if w := reloadWarning(k); len(w) != 1 {
		t.Fatalf("proposed counts as unconfirmed: %v", w)
	}
	k.Reload[0].Proposed = false
	if w := reloadWarning(k); len(w) != 0 {
		t.Fatalf("recorded: %v", w)
	}
	k.Entry = &knowledge.Entry{None: true}
	k.Reload = nil
	if w := reloadWarning(k); len(w) != 0 {
		t.Fatalf("no entry: %v", w)
	}
}

func TestMergeKeepsRecordedReload(t *testing.T) {
	existing := []knowledge.Reload{{Service: "web", Mode: "hot"}, {Service: "api", Mode: "rebuild", Proposed: true}}
	fresh := []knowledge.Reload{{Service: "web", Mode: "rebuild", Proposed: true}, {Service: "api", Mode: "restart", Proposed: true}}
	dir := t.TempDir()
	path := dir + "/knowledge.json"
	old := &knowledge.Knowledge{Status: knowledge.StatusDraft, Reload: existing}
	if err := old.Save(path); err != nil {
		t.Fatal(err)
	}
	f := &knowledge.Knowledge{Reload: fresh}
	mergeKnowledgeFile(dir, path, f)
	if len(f.Reload) != 2 || f.ReloadFor("web").Mode != "hot" || f.ReloadFor("api").Mode != "restart" {
		t.Fatalf("%+v", f.Reload)
	}
}

func TestLearnVerifyWarnsUnconfirmedReload(t *testing.T) {
	web := newProbeServer(func(string) int { return 200 })
	defer web.Close()
	v := newVerifyEnv(t, serverPort(t, web), closedPort(t), &knowledge.Entry{Service: "web", Port: 8080}, nil)
	out, stderr, code := v.verify(t)
	if code != 0 || !strings.Contains(out, "learn record reload web") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	k, _ := knowledge.Load(v.path)
	k.Reload = []knowledge.Reload{{Service: "web", Mode: "hot"}}
	if err := k.Save(v.path); err != nil {
		t.Fatal(err)
	}
	if out, _, code := v.verify(t); code != 0 || strings.Contains(out, "learn record reload") {
		t.Fatalf("recorded reload still warns: %s", out)
	}
}
