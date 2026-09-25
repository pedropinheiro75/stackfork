package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestImageKindMapping covers task 3.5's check: official images, the
// "library/" prefix, any registry host, tags and digests all reduce to the
// same logo key, and an org/repo image (minio/minio) still resolves by its
// last path segment.
func TestImageKindMapping(t *testing.T) {
	cases := map[string]string{
		"postgres:17":                             "postgres",
		"postgres":                                "postgres",
		"library/postgres:17":                     "postgres",
		"docker.io/library/postgres:17":           "postgres",
		"postgres@sha256:abc123def456":            "postgres",
		"redis:7-alpine":                          "redis",
		"minio/minio:latest":                      "minio",
		"minio/minio":                             "minio",
		"gotenberg/gotenberg:8":                   "gotenberg",
		"rnwood/smtp4dev:latest":                  "smtp4dev",
		"axllent/mailpit":                         "mailpit",
		"maildev/maildev":                         "maildev",
		"myregistry.example.com:5000/postgres:17": "postgres",
		"ghcr.io/myorg/custom-app:latest":         "",
		"":                                        "",
	}
	for image, want := range cases {
		if got := imageKind(image); got != want {
			t.Errorf("imageKind(%q) = %q, want %q", image, got, want)
		}
	}
}

// TestFillServiceDetailsFromSnapshot covers task 3.4: ports, volumes,
// healthcheck, depends_on, replicas and env key NAMES are read from the
// snapshot, and the canary check that no env value ever appears in the
// inventory's own JSON.
func TestFillServiceDetailsFromSnapshot(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	const canary = "canary-env-value-3-4"
	config := map[string]interface{}{
		"services": map[string]interface{}{
			"db": map[string]interface{}{
				"image": "postgres:17",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("5432"), "published": "20005", "protocol": "tcp"},
				},
				"volumes": []interface{}{
					map[string]interface{}{"type": "volume", "source": "pgdata", "target": "/var/lib/postgresql/data"},
					map[string]interface{}{"type": "bind", "source": "/host/init", "target": "/docker-entrypoint-initdb.d", "read_only": true},
				},
				"healthcheck": map[string]interface{}{
					"test": []interface{}{"CMD-SHELL", "pg_isready -U appuser"},
				},
				"deploy": map[string]interface{}{
					"replicas": json.Number("2"),
				},
				"environment": map[string]interface{}{
					"POSTGRES_PASSWORD": canary,
					"POSTGRES_USER":     "appuser",
					"POSTGRES_DB":       "appdb",
				},
			},
			"web": map[string]interface{}{
				"build": map[string]interface{}{"context": "/nowhere"},
				"depends_on": map[string]interface{}{
					"db": map[string]interface{}{"condition": "service_healthy"},
				},
			},
		},
	}

	project := "sf-details-1"
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
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == project {
			inst = i
		}
	}
	var db, web Service
	for _, s := range inst.Services {
		switch s.Name {
		case "db":
			db = s
		case "web":
			web = s
		}
	}

	if db.Image != "postgres:17" || db.Kind != "postgres" {
		t.Errorf("db: image=%q kind=%q", db.Image, db.Kind)
	}
	if len(db.Ports) != 1 || db.Ports[0].Container != 5432 || db.Ports[0].Host != 20005 {
		t.Errorf("db: unexpected ports %+v", db.Ports)
	}
	if len(db.Volumes) != 2 || db.Volumes[0].Name != "pgdata" || db.Volumes[1].ReadOnly != true {
		t.Errorf("db: unexpected volumes %+v", db.Volumes)
	}
	if db.Healthcheck != "pg_isready -U appuser" {
		t.Errorf("db: healthcheck = %q", db.Healthcheck)
	}
	if db.Replicas != 2 {
		t.Errorf("db: replicas = %d", db.Replicas)
	}
	want := []string{"POSTGRES_DB", "POSTGRES_PASSWORD", "POSTGRES_USER"}
	if len(db.EnvKeys) != len(want) {
		t.Errorf("db: env keys = %+v, want %+v", db.EnvKeys, want)
	} else {
		for i, k := range want {
			if db.EnvKeys[i] != k {
				t.Errorf("db: env keys = %+v, want %+v", db.EnvKeys, want)
				break
			}
		}
	}
	if len(web.DependsOn) != 1 || web.DependsOn[0] != "db" {
		t.Errorf("web: depends_on = %+v", web.DependsOn)
	}
	if web.Kind != "" {
		t.Errorf("web: expected empty kind for an unresolvable build, got %q", web.Kind)
	}

	// Canary: the env VALUE never appears anywhere in the inventory's own
	// JSON, only the key names do.
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), canary) {
		t.Fatalf("inventory JSON leaked an environment value:\n%s", b)
	}
	if !strings.Contains(string(b), "POSTGRES_PASSWORD") {
		t.Fatalf("expected the env key name to appear, got:\n%s", b)
	}
}
