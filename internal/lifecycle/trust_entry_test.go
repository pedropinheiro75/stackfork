package lifecycle

import (
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// Spec "Edited link after trust": a trusted plan whose knowledge file gains a
// link, or a changed entry, is still trusted.
func TestTrustSurvivesNewLinkAndEntry(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	k := &knowledge.Knowledge{Status: knowledge.StatusDraft, Plan: knowledge.Plan{
		Status: knowledge.PlanRecorded,
		Steps:  []knowledge.Step{{ID: "app", Kind: knowledge.KindHost, Name: "app", Cmd: "bun run dev", Port: 3000}},
	}}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := knowledge.WriteTrust(knowledge.TrustPath(base, root), knowledge.PlanHash(k.Plan.Steps), knowledge.TrustSteps(k.Plan.Steps)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := HashTrusted(base, root, k.Plan); !ok {
		t.Fatal("not trusted after approval")
	}
	k.Entry = &knowledge.Entry{Service: "web", Port: 3000, Path: "/app"}
	k.Links = []knowledge.Link{{Name: "Dashboard", Path: "/admin"}}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := knowledge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, diff := HashTrusted(base, root, got.Plan); !ok {
		t.Fatalf("a link or entry broke trust: %v", diff)
	}
}
