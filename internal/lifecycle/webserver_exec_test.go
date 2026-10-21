package lifecycle

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const webStack = `{"services":{"app":{"build":{"context":"."}},"db":{"image":"postgres:17"},"nginx":{"image":"nginx:1.27","ports":[{"target":80,"published":"8080"}]}}}`

func webStackDir(t *testing.T, services string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.json"), []byte(services), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A web server or proxy that holds the HTTP route is no place to run the
// app's command.
func TestResolveExecServiceNeverPicksAWebServer(t *testing.T) {
	for _, image := range []string{"nginx:1.27", "caddy:2", "traefik:v3", "httpd:2.4", "haproxy:3", "envoyproxy/envoy:v1", "openresty/openresty:alpine", "docker.io/library/nginx"} {
		dir := webStackDir(t, `{"services":{"db":{"image":"postgres:17"},"edge":{"image":"`+image+`","ports":[{"target":80}]}}}`)
		if got := ResolveExecService(dir, knowledge.Step{}, knowledge.Plan{}); got != "" {
			t.Errorf("%s: resolved %q", image, got)
		}
	}
	// the only service is a web server: still not a place for the command
	if got := ResolveExecService(webStackDir(t, `{"services":{"web":{"image":"nginx"}}}`), knowledge.Step{}, knowledge.Plan{}); got != "" {
		t.Errorf("single nginx: resolved %q", got)
	}
	// explicit wins
	if got := ResolveExecService(webStackDir(t, webStack), knowledge.Step{Service: "nginx"}, knowledge.Plan{}); got != "nginx" {
		t.Errorf("explicit: %q", got)
	}
}

func upSeedFixture(t *testing.T) UpSeed {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"db:migrate":"x","db:seed":"y"}}`), 0o600)
	os.WriteFile(filepath.Join(root, "bun.lock"), nil, 0o600)
	return UpSeed{Root: root, Main: root, Branch: "b", Project: "p", InstanceDir: webStackDir(t, webStack), Mode: knowledge.ModeContainer,
		Seed: true, FreshData: true, Compose: func(_, _ io.Writer) (int, error) { return 0, nil }}
}

// An auto-detected seed that no service can run is skipped with a note: the
// stack is up and the person decides.
func TestUpSkipsAnAutoDetectedSeedNoServiceCanRun(t *testing.T) {
	a := upSeedFixture(t)
	var out, errOut bytes.Buffer
	res := UpAndSeed(&out, &errOut, a)
	if res.Err != nil || res.SeedErr != nil || res.Code != 0 || res.Seeded {
		t.Fatalf("up must succeed without seeding: %+v", res)
	}
	all := out.String() + errOut.String()
	for _, want := range []string{"seed not run: no service can run `", "bun run db:seed", "stackfork learn record step"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %q", want, all)
		}
	}
}
