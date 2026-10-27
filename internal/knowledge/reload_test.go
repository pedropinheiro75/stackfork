package knowledge

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReloadRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.json")
	k := &Knowledge{Reload: []Reload{{Service: "web", Mode: ReloadHot, Proposed: true, Evidence: "compose.yaml:3"}}}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if r := got.ReloadFor("web"); r == nil || *r != k.Reload[0] || got.ReloadFor("db") != nil {
		t.Fatalf("%+v", got.Reload)
	}
	if !strings.Contains(got.Markdown(), "- web: hot (proposed) [compose.yaml:3]") {
		t.Errorf("markdown lacks the reload line:\n%s", got.Markdown())
	}
	for name, list := range map[string][]Reload{
		"bad mode":  {{Service: "web", Mode: "sometimes"}},
		"bad name":  {{Service: "w e b", Mode: ReloadHot}},
		"duplicate": {{Service: "web", Mode: ReloadHot}, {Service: "web", Mode: ReloadRestart}},
	} {
		if (&Knowledge{Reload: list}).Save(path) == nil {
			t.Errorf("%s: Save accepted it", name)
		}
	}
}
