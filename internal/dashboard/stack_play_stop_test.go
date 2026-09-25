package dashboard_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestStackPlayStopButtonsMatchState covers fix 4: a stopped stack shows
// only the play button, a fully running one shows only stop, and a
// partially running one shows both - never both on a stack with nothing
// running.
func TestStackPlayStopButtonsMatchState(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()

	write := func(project string, services []string) {
		root := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", "-b", "main", root).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
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
		svcMap := map[string]interface{}{}
		for _, s := range services {
			svcMap[s] = map[string]interface{}{}
		}
		config := map[string]interface{}{"services": svcMap}
		if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
			t.Fatal(err)
		}
	}

	write("sf-stopped-1", []string{"web"})
	write("sf-running-1", []string{"web"})
	write("sf-partial-1", []string{"web", "db"})

	psLine := func(project, service, state string) string {
		labels := "com.docker.compose.project=" + project + ",com.docker.compose.service=" + service + ",io.stackfork.root=/x"
		row := map[string]string{"Labels": labels, "State": state, "Status": "Up", "Ports": ""}
		b, _ := json.Marshal(row)
		return string(b)
	}
	psLines := []string{
		psLine("sf-running-1", "web", "running"),
		psLine("sf-partial-1", "web", "running"),
		psLine("sf-partial-1", "db", "exited"),
	}
	t.Setenv("DOCKER_FAKE_PS", strings.Join(psLines, "\n")+"\n")

	srv, err := dashboard.New(newBase, legacyBase, "test", 55901)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55901"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	stackButtons := func(project string) (hasUp, hasDown bool) {
		start := strings.Index(body, `data-project="`+project+`"`)
		if start < 0 {
			t.Fatalf("stack %s not found in body", project)
		}
		end := strings.Index(body[start:], "</header>") + start
		section := body[start:end]
		return strings.Contains(section, `data-action="up"`), strings.Contains(section, `data-action="down"`)
	}

	if up, down := stackButtons("sf-stopped-1"); !up || down {
		t.Errorf("stopped: expected up-only, got up=%v down=%v", up, down)
	}
	if up, down := stackButtons("sf-running-1"); up || !down {
		t.Errorf("running: expected down-only, got up=%v down=%v", up, down)
	}
	if up, down := stackButtons("sf-partial-1"); !up || !down {
		t.Errorf("partial: expected both, got up=%v down=%v", up, down)
	}
}
