package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestUseCommandsPerKind covers task 3.10: the per-kind use-it commands
// (design.md D3b §3), all placeholder-safe.
func TestUseCommandsPerKind(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	const canary = "canary-usecommands-password"
	config := map[string]interface{}{
		"services": map[string]interface{}{
			"pg": map[string]interface{}{
				"image": "postgres:17",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("5432"), "published": "20070", "protocol": "tcp"},
				},
				"environment": map[string]interface{}{"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb", "POSTGRES_PASSWORD": canary},
			},
			"mysqldb": map[string]interface{}{
				"image": "mysql:8",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("3306"), "published": "20071", "protocol": "tcp"},
				},
				"environment": map[string]interface{}{"MYSQL_USER": "appuser", "MYSQL_DATABASE": "appdb", "MYSQL_PASSWORD": canary},
			},
			"cache": map[string]interface{}{
				"image": "redis:7",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("6379"), "published": "20072", "protocol": "tcp"},
				},
			},
			"mail": map[string]interface{}{
				"image": "axllent/mailpit:latest",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("8025"), "published": "20073", "protocol": "tcp"},
					map[string]interface{}{"target": json.Number("1025"), "published": "20074", "protocol": "tcp"},
				},
			},
			"mongodb": map[string]interface{}{
				"image": "mongo:7",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("27017"), "published": "20075", "protocol": "tcp"},
				},
			},
		},
	}
	project := "sf-usecmd-1"
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

	pg := byName["pg"]
	if len(pg.UseCommands) != 2 {
		t.Fatalf("postgres: expected 2 use commands, got %+v", pg.UseCommands)
	}
	if pg.UseCommands[0].Value != `psql "postgres://appuser:<password>@127.0.0.1:20070/appdb"` {
		t.Errorf("postgres psql command: %q", pg.UseCommands[0].Value)
	}
	if pg.UseCommands[1].Value != "stackfork exec pg psql -U appuser appdb" {
		t.Errorf("postgres exec command: %q", pg.UseCommands[1].Value)
	}

	mysqldb := byName["mysqldb"]
	if len(mysqldb.UseCommands) != 2 {
		t.Fatalf("mysql: expected 2 use commands, got %+v", mysqldb.UseCommands)
	}
	if mysqldb.UseCommands[0].Value != "mysql -h 127.0.0.1 -P 20071 -u appuser -p<password> appdb" {
		t.Errorf("mysql command: %q", mysqldb.UseCommands[0].Value)
	}

	cache := byName["cache"]
	if len(cache.UseCommands) != 1 || cache.UseCommands[0].Value != "redis-cli -h 127.0.0.1 -p 20072" {
		t.Errorf("redis command: %+v", cache.UseCommands)
	}

	mail := byName["mail"]
	if len(mail.UseCommands) != 2 || mail.UseCommands[0].Label != "SMTP_HOST" || mail.UseCommands[1].Value != "20074" {
		t.Errorf("mail SMTP commands: %+v", mail.UseCommands)
	}

	mongodb := byName["mongodb"]
	if len(mongodb.UseCommands) != 0 {
		t.Errorf("mongo: expected no extra use commands, got %+v", mongodb.UseCommands)
	}

	// Canary: no password value anywhere.
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), canary) {
		t.Fatalf("inventory JSON leaked a password value")
	}
}

// TestEnvBlockCoversAllKinds covers task 3.10's per-stack env block.
func TestEnvBlockCoversAllKinds(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	config := map[string]interface{}{
		"services": map[string]interface{}{
			"pg": map[string]interface{}{
				"image": "postgres:17",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("5432"), "published": "20080", "protocol": "tcp"},
				},
			},
			"cache": map[string]interface{}{
				"image": "redis:7",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("6379"), "published": "20081", "protocol": "tcp"},
				},
			},
			"mail": map[string]interface{}{
				"image": "axllent/mailpit:latest",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("8025"), "published": "20082", "protocol": "tcp"},
					map[string]interface{}{"target": json.Number("1025"), "published": "20083", "protocol": "tcp"},
				},
			},
		},
	}
	project := "sf-envblock-1"
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
	for _, want := range []string{"DATABASE_URL=postgres://postgres:<password>@127.0.0.1:20080/postgres", "REDIS_URL=redis://127.0.0.1:20081", "SMTP_HOST=127.0.0.1", "SMTP_PORT=20083"} {
		if !strings.Contains(inst.EnvBlock, want) {
			t.Errorf("expected env block to contain %q, got:\n%s", want, inst.EnvBlock)
		}
	}
}
