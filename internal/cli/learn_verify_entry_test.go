package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const verifyCompose = `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:8080"
    labels:
      ` + identity.LabelHTTP + `: "8080"
  mail:
    image: rnwood/smtp4dev
    ports:
      - "8025:80"
`

type verifyEnv struct {
	root  string
	flags []string
	path  string
	host  string
	// project is the prepared instance's name.
	project string
}

func serverPort(t *testing.T, s *probeServer) int {
	t.Helper()
	_, p, _ := net.SplitHostPort(s.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

// closedPort is a port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// newVerifyEnv prepares a stack with a web and a mail service, records a
// plan with a compose-up step and the given entry and links, and makes the
// fake docker report both services running on the given host ports.
func newVerifyEnv(t *testing.T, webPort, mailPort int, e *knowledge.Entry, links []knowledge.Link) verifyEnv {
	t.Helper()
	installFakeDocker(t)
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(verifyCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--state-dir", filepath.Join(t.TempDir(), "state"), "--legacy-state-dir", t.TempDir()}
	out, stderr, code := runCLI(t, append(append([]string{}, flags...), "--root", root, "prepare", "-f", filepath.Join(root, "compose.yaml"))...)
	if code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	var prepared map[string]interface{}
	json.Unmarshal([]byte(out), &prepared)
	host, _ := prepared["hostname"].(string)
	project, _ := prepared["project"].(string)
	k := &knowledge.Knowledge{Status: knowledge.StatusDraft, Entry: e, Links: links, Plan: knowledge.Plan{
		Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}},
	}}
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	rows := func(svc string, target, published int) string {
		return fmt.Sprintf(`{"ID":"c-%s","Name":"%s-1","Service":"%s","State":"running","Health":"","Publishers":[{"Protocol":"tcp","PublishedPort":%d,"TargetPort":%d,"URL":"127.0.0.1"}]}`, svc, svc, svc, published, target)
	}
	t.Setenv("DOCKER_FAKE_STATUS", rows("web", 8080, webPort)+"\n"+rows("mail", 80, mailPort)+"\n")
	return verifyEnv{root: root, flags: flags, path: path, host: host, project: project}
}

func (v verifyEnv) verify(t *testing.T) (string, string, int) {
	t.Helper()
	return runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "learn", "verify", "--check", "app loads", "--confirm", "yes")...)
}

type probeServer struct {
	*httptest.Server
	mu    sync.Mutex
	hosts []string
	paths []string
}

func newProbeServer(status func(path string) int) *probeServer {
	p := &probeServer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hosts = append(p.hosts, r.Host)
		p.paths = append(p.paths, r.URL.RequestURI())
		p.mu.Unlock()
		if st := status(r.URL.Path); st >= 300 && st < 400 {
			w.Header().Set("Location", "/login")
			w.WriteHeader(st)
			return
		}
		w.WriteHeader(status(r.URL.Path))
	}))
	return p
}

// entry-links task 3.3: the entry has to answer before verify succeeds.
func TestLearnVerifyProbesEntry(t *testing.T) {
	for _, tc := range []struct {
		status int
		ok     bool
	}{{200, true}, {302, true}, {401, true}, {404, true}, {502, false}, {500, false}} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			web := newProbeServer(func(string) int { return tc.status })
			defer web.Close()
			v := newVerifyEnv(t, serverPort(t, web), closedPort(t), &knowledge.Entry{Service: "web", Port: 8080, Path: "/app"}, nil)
			out, stderr, code := v.verify(t)
			k, err := knowledge.Load(v.path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.ok {
				if code != 0 || k.Status != knowledge.StatusVerified || k.Verification.Entry != "web:8080/app" {
					t.Fatalf("%d %s %s %+v", code, out, stderr, k.Verification)
				}
				if len(web.hosts) != 1 || !strings.HasPrefix(web.hosts[0], v.host) || web.paths[0] != "/app" {
					t.Fatalf("probe sent %v %v, want host %s and /app", web.hosts, web.paths, v.host)
				}
				return
			}
			if code == 0 || k.Status != knowledge.StatusDraft || !strings.Contains(stderr, "web:8080/app") || !strings.Contains(stderr, strconv.Itoa(tc.status)) {
				t.Fatalf("%d %s %s", code, out, stderr)
			}
		})
	}
}

func TestLearnVerifyEntryRefusals(t *testing.T) {
	web := newProbeServer(func(string) int { return 200 })
	defer web.Close()
	for name, tc := range map[string]struct {
		entry *knowledge.Entry
		want  string
		stop  bool
	}{
		"no entry":     {nil, "record the app's entry first", false},
		"proposed":     {&knowledge.Entry{Service: "web", Port: 8080, Proposed: true}, "record the app's entry first", false},
		"not in stack": {&knowledge.Entry{Service: "caddy", Port: 80}, "not in this stack", false},
		"closed port":  {&knowledge.Entry{Service: "mail", Port: 80}, "did not answer", false},
		"stopped":      {&knowledge.Entry{Service: "web", Port: 8080}, "not running", true},
	} {
		t.Run(name, func(t *testing.T) {
			v := newVerifyEnv(t, serverPort(t, web), closedPort(t), tc.entry, nil)
			if tc.stop {
				t.Setenv("DOCKER_FAKE_STATUS", "")
			}
			_, stderr, code := v.verify(t)
			if code == 0 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("%d %s", code, stderr)
			}
			if k, _ := knowledge.Load(v.path); k.Status != knowledge.StatusDraft {
				t.Fatalf("status %s", k.Status)
			}
		})
	}
}

func TestLearnVerifyNoneSkipsProbe(t *testing.T) {
	v := newVerifyEnv(t, closedPort(t), closedPort(t), &knowledge.Entry{None: true}, nil)
	t.Setenv("DOCKER_FAKE_STATUS", "") // not even running
	out, stderr, code := v.verify(t)
	if code != 0 {
		t.Fatalf("%s %s", out, stderr)
	}
	if k, _ := knowledge.Load(v.path); k.Verification.Entry != "none" || len(k.Verification.Links) != 0 {
		t.Fatalf("%+v", k.Verification)
	}
}

func TestLearnVerifyProbesLinks(t *testing.T) {
	web := newProbeServer(func(p string) int {
		if p == "/admin" {
			return 404
		}
		return 200
	})
	defer web.Close()
	mail := newProbeServer(func(string) int { return 200 })
	defer mail.Close()
	links := []knowledge.Link{
		{Name: "Dashboard", Path: "/admin"},
		{Name: "Email", Service: "mail", Port: 80},
		{Name: "Gone", Service: "mail", Port: 25},
	}
	v := newVerifyEnv(t, serverPort(t, web), serverPort(t, mail), &knowledge.Entry{Service: "web", Port: 8080}, links)
	out, stderr, code := v.verify(t)
	if code != 0 {
		t.Fatalf("%s %s", out, stderr)
	}
	k, _ := knowledge.Load(v.path)
	got := fmt.Sprint(k.Verification.Links)
	if got != "[{Dashboard true 404} {Email true 200} {Gone false 0}]" {
		t.Fatalf("%s", got)
	}
	if !strings.Contains(out, `"warnings"`) || !strings.Contains(out, "Gone") {
		t.Fatalf("%s", out)
	}
	if k.Verification.Entry != "web:8080" {
		t.Fatalf("%s", k.Verification.Entry)
	}

	// The mail UI is down: verify still succeeds, with a warning for it.
	mail.Close()
	v2 := newVerifyEnv(t, serverPort(t, web), serverPort(t, mail), &knowledge.Entry{Service: "web", Port: 8080}, links[1:2])
	out, _, code = v2.verify(t)
	k2, _ := knowledge.Load(v2.path)
	if code != 0 || !strings.Contains(out, "Email") || k2.Verification.Links[0].Answered {
		t.Fatalf("%d %s %+v", code, out, k2.Verification)
	}
}
