package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestConnectionURLNeverIncludesAPassword covers task 3.7: a Postgres,
// MySQL, Redis and Mongo service each get a copy-paste connection string
// with a literal <password> placeholder, even when the compose file sets a
// real plain-text password - that value must never appear anywhere in the
// inventory's own JSON.
func TestConnectionURLNeverIncludesAPassword(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	const pgPassword = "canary-pg-password-3-7"
	const mysqlPassword = "canary-mysql-password-3-7"
	const redisPassword = "canary-redis-password-3-7"
	const mongoPassword = "canary-mongo-password-3-7"

	config := map[string]interface{}{
		"services": map[string]interface{}{
			"pg": map[string]interface{}{
				"image": "postgres:17",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("5432"), "published": "20010", "protocol": "tcp"},
				},
				"environment": map[string]interface{}{
					"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb", "POSTGRES_PASSWORD": pgPassword,
				},
			},
			"mysqldb": map[string]interface{}{
				"image": "mysql:8",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("3306"), "published": "20011", "protocol": "tcp"},
				},
				"environment": map[string]interface{}{
					"MYSQL_USER": "appuser", "MYSQL_DATABASE": "appdb",
					"MYSQL_PASSWORD": mysqlPassword, "MYSQL_ROOT_PASSWORD": mysqlPassword,
				},
			},
			"cache": map[string]interface{}{
				"image": "redis:7",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("6379"), "published": "20012", "protocol": "tcp"},
				},
				"environment": map[string]interface{}{"REDIS_PASSWORD": redisPassword},
			},
			"cachepass": map[string]interface{}{
				"image":   "redis:7",
				"command": []interface{}{"redis-server", "--requirepass", "${REDIS_PW}"},
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("6379"), "published": "20014", "protocol": "tcp"},
				},
			},
			"mongodb": map[string]interface{}{
				"image": "mongo:7",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("27017"), "published": "20013", "protocol": "tcp"},
				},
				"environment": map[string]interface{}{
					"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_DATABASE": "appdb",
					"MONGO_INITDB_ROOT_PASSWORD": mongoPassword,
				},
			},
			"norandom": map[string]interface{}{
				// No published host port at all (not running, random-ports):
				// connection string must stay empty rather than fabricate one.
				"image": "postgres:17",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("5432"), "protocol": "tcp"},
				},
			},
		},
	}

	project := "sf-conn-1"
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
	byName := map[string]Service{}
	for _, s := range inst.Services {
		byName[s.Name] = s
	}

	cases := []struct {
		service, want string
	}{
		{"pg", "postgres://appuser:<password>@127.0.0.1:20010/appdb"},
		{"mysqldb", "mysql://appuser:<password>@127.0.0.1:20011/appdb"},
		{"cache", "redis://:<password>@127.0.0.1:20012"},
		{"cachepass", "redis://:<password>@127.0.0.1:20014"},
		{"mongodb", "mongodb://root:<password>@127.0.0.1:20013/appdb"},
	}
	for _, c := range cases {
		svc, ok := byName[c.service]
		if !ok {
			t.Fatalf("service %s not found", c.service)
		}
		if svc.ConnectionURL != c.want {
			t.Errorf("%s: ConnectionURL = %q, want %q", c.service, svc.ConnectionURL, c.want)
		}
	}
	if svc := byName["norandom"]; svc.ConnectionURL != "" {
		t.Errorf("norandom: expected empty ConnectionURL for an unresolved port, got %q", svc.ConnectionURL)
	}

	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{pgPassword, mysqlPassword, redisPassword, mongoPassword} {
		if strings.Contains(string(b), canary) {
			t.Fatalf("inventory JSON leaked a password value %q", canary)
		}
	}
	// The exact per-service ConnectionURL assertions above already prove
	// the placeholder itself is "<password>"; here just confirm the word
	// made it into the marshaled JSON (json.Marshal HTML-escapes '<'/'>',
	// so a literal substring match on "<password>" would be brittle).
	if !strings.Contains(string(b), "password") {
		t.Fatalf("expected the password placeholder to appear, got:\n%s", b)
	}
}

// TestConnectionURLEmptyForUnknownKind covers the "not a database" case:
// no connection string, no error.
func TestConnectionURLEmptyForUnknownKind(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	config := map[string]interface{}{
		"services": map[string]interface{}{
			"web": map[string]interface{}{
				"image": "nginx:alpine",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("80"), "published": "20020", "protocol": "tcp"},
				},
			},
		},
	}
	project := "sf-conn-2"
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
	for _, i := range inv.Instances {
		if i.Project != project {
			continue
		}
		for _, s := range i.Services {
			if s.ConnectionURL != "" {
				t.Fatalf("expected no connection URL for nginx, got %q", s.ConnectionURL)
			}
		}
	}
}
