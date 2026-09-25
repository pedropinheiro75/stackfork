package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestMailCatcherHTTPPortByKind covers fix 3: a mail catcher's known web UI
// port renders as HTTP, its SMTP port stays tcp, and an unrecognized image
// with the same port number is never guessed at.
func TestMailCatcherHTTPPortByKind(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	config := map[string]interface{}{
		"services": map[string]interface{}{
			"mailpit": map[string]interface{}{
				"image": "axllent/mailpit:latest",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("8025"), "published": "20040", "protocol": "tcp"},
					map[string]interface{}{"target": json.Number("1025"), "published": "20041", "protocol": "tcp"},
				},
			},
			"unknown": map[string]interface{}{
				// Same port number (8025) but not a recognized mail
				// catcher image: must stay tcp ("don't guess").
				"image": "ghcr.io/example/custom-app:latest",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("8025"), "published": "20042", "protocol": "tcp"},
				},
			},
		},
	}
	project := "sf-mailport-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}

	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Service{}
	for _, i := range inv.Instances {
		if i.Project != project {
			continue
		}
		for _, s := range i.Services {
			byName[s.Name] = s
		}
	}

	mailpit, ok := byName["mailpit"]
	if !ok {
		t.Fatal("mailpit service not found")
	}
	if mailpit.Kind != "mail" {
		t.Errorf("mailpit: kind = %q, want mail", mailpit.Kind)
	}
	schemeByPort := map[int]string{}
	for _, e := range mailpit.Endpoints {
		schemeByPort[e.Container] = e.Scheme
	}
	if schemeByPort[8025] != "http" {
		t.Errorf("mailpit: expected port 8025 to be http, got %q", schemeByPort[8025])
	}
	if schemeByPort[1025] != "tcp" {
		t.Errorf("mailpit: expected port 1025 (SMTP) to stay tcp, got %q", schemeByPort[1025])
	}

	unknown, ok := byName["unknown"]
	if !ok {
		t.Fatal("unknown service not found")
	}
	if unknown.Kind != "" {
		t.Errorf("unknown: expected empty kind, got %q", unknown.Kind)
	}
	if len(unknown.Endpoints) != 1 || unknown.Endpoints[0].Scheme != "tcp" {
		t.Errorf("unknown: expected port 8025 to stay tcp for an unrecognized image, got %+v", unknown.Endpoints)
	}
}
