package inventory

import (
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestFillReload(t *testing.T) {
	root := t.TempDir()
	k := &knowledge.Knowledge{Reload: []knowledge.Reload{
		{Service: "web", Mode: "rebuild"}, {Service: "app", Mode: "hot", Proposed: true},
	}}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	svcs := []Service{{Name: "web"}, {Name: "db"}, {Name: "host:app", Label: "app", Kind: "host"}}
	fillReload(svcs, root)
	if svcs[0].Reload != "rebuild" || svcs[0].ReloadLabel != "rebuild on change" {
		t.Errorf("web: %+v", svcs[0])
	}
	if svcs[1].Reload != "" || svcs[1].ReloadLabel != "" {
		t.Errorf("db has no entry: %+v", svcs[1])
	}
	if svcs[2].Reload != "hot" || svcs[2].ReloadHint == "" {
		t.Errorf("host app is keyed by its label: %+v", svcs[2])
	}
}
