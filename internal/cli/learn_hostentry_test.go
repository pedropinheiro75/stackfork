package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// hostEntryEnv is a verifyEnv whose plan has a host step "app" and whose
// manifest has the given host ports and routes.
func hostEntryEnv(t *testing.T, e *knowledge.Entry, hostPorts, routes map[string]int) verifyEnv {
	t.Helper()
	v := newVerifyEnv(t, closedPort(t), closedPort(t), e, nil)
	k, err := knowledge.Load(v.path)
	if err != nil {
		t.Fatal(err)
	}
	k.Plan.Steps = append(k.Plan.Steps, knowledge.Step{ID: "app", Kind: knowledge.KindHost, Name: "app", Cmd: "x", Modes: []string{knowledge.ModeHybrid}})
	if err := k.Save(v.path); err != nil {
		t.Fatal(err)
	}
	manifests, _ := filepath.Glob(filepath.Join(v.flags[1], "*", "manifest.json"))
	if len(manifests) == 0 {
		manifests, _ = filepath.Glob(filepath.Join(v.flags[1], "*", "*", "manifest.json"))
	}
	if len(manifests) != 1 {
		t.Fatalf("manifests %v", manifests)
	}
	m, _ := state.ReadManifest(manifests[0])
	m.HostPorts, m.Routes = hostPorts, routes
	data, _ := json.Marshal(m)
	if err := os.WriteFile(manifests[0], data, 0o600); err != nil {
		t.Fatal(err)
	}
	return v
}

func (v verifyEnv) record(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runCLI(t, append(append([]string{}, v.flags...), append([]string{"--root", v.root, "learn", "record"}, args...)...)...)
}

func TestLearnRecordEntryHostApp(t *testing.T) {
	v := hostEntryEnv(t, nil, map[string]int{"app": 41234}, nil)
	if out, stderr, code := v.record(t, "entry", "--service", "app"); code != 0 {
		t.Fatalf("no port: %d %s %s", code, out, stderr)
	}
	if k, _ := knowledge.Load(v.path); k.Entry == nil || *k.Entry != (knowledge.Entry{Service: "app", Port: 41234}) {
		t.Fatalf("%+v", k.Entry)
	}
	if out, stderr, code := v.record(t, "entry", "--service", "app", "--port", "4000", "--path", "/x"); code != 0 {
		t.Fatalf("port: %d %s %s", code, out, stderr)
	}
	if k, _ := knowledge.Load(v.path); *k.Entry != (knowledge.Entry{Service: "app", Port: 4000, Path: "/x"}) {
		t.Fatalf("%+v", k.Entry)
	}
	if _, stderr, code := v.record(t, "link", "Site", "--service", "app"); code != 0 {
		t.Fatalf("link: %s", stderr)
	}
	if k, _ := knowledge.Load(v.path); len(k.Links) != 1 || k.Links[0].Service != "app" || k.Links[0].Port != 41234 {
		t.Fatalf("%+v", k.Links)
	}
}

func TestLearnRecordEntryHostAppNeedsPort(t *testing.T) {
	v := hostEntryEnv(t, nil, nil, nil)
	if _, stderr, code := v.record(t, "entry", "--service", "app"); code == 0 || !strings.Contains(stderr, "--port") {
		t.Fatalf("%d %s", code, stderr)
	}
	if _, stderr, code := v.record(t, "entry", "--service", "web"); code == 0 || !strings.Contains(stderr, "usage") {
		t.Fatalf("compose service without port: %d %s", code, stderr)
	}
}

func TestLearnRecordEntryRefusalListsBothKinds(t *testing.T) {
	v := hostEntryEnv(t, nil, map[string]int{"app": 41234}, nil)
	_, stderr, code := v.record(t, "entry", "--service", "ghost", "--port", "80")
	if code == 0 || !strings.Contains(stderr, "Compose services: ") || !strings.Contains(stderr, "web (8080)") || !strings.Contains(stderr, "host apps: app") {
		t.Fatalf("%d %s", code, stderr)
	}
}

func TestLearnVerifyProbesHostAppEntry(t *testing.T) {
	app := newProbeServer(func(string) int { return 200 })
	defer app.Close()
	p := serverPort(t, app)
	v := hostEntryEnv(t, &knowledge.Entry{Service: "app", Port: p, Path: "/home"}, map[string]int{"app": p}, map[string]int{"app": p})
	out, stderr, code := v.verify(t)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	if len(app.hosts) != 1 || !strings.HasPrefix(app.hosts[0], v.host) || app.paths[0] != "/home" {
		t.Fatalf("probe sent %v %v", app.hosts, app.paths)
	}
	if k, _ := knowledge.Load(v.path); !strings.Contains(k.Verification.Entry, "app") {
		t.Fatalf("%+v", k.Verification)
	}
}

func TestLearnVerifyStoppedHostAppEntry(t *testing.T) {
	v := hostEntryEnv(t, &knowledge.Entry{Service: "app", Port: 41234}, map[string]int{"app": 41234}, nil)
	_, stderr, code := v.verify(t)
	if code == 0 || !strings.Contains(stderr, "host app app") || !strings.Contains(stderr, "start the instance") {
		t.Fatalf("%d %s", code, stderr)
	}
}
