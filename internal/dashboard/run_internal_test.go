package dashboard

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlanRunAllowlist(t *testing.T) {
	old := worktreeClean
	defer func() { worktreeClean = old }()
	worktreeClean = func(string) bool { return true }
	s := &Server{}
	wt := func(repo, path string) (bool, string) { return repo == "/r" && path == "/r/.worktrees/a", "sf-a-1" }
	refused := []string{
		"rm -rf /", "stackfork config add-root /", "stackfork up; rm -rf /", "stackfork up && echo x",
		"stackfork prepare -f x.yaml", "stackfork --state-dir /tmp up", "stackfork learn record recipe --seed-once x",
		"git -C /r worktree remove --force /r/.worktrees/a", "git -C /r worktree remove /r", "git push",
		"docker rm postgres", "docker volume rm x", "stackfork proxy down", "stackfork up $(id)",
	}
	for _, c := range refused {
		if p, err := s.planRun(c, "", wt); err == nil {
			t.Errorf("%q allowed: %+v", c, p)
		}
	}
	cases := []struct {
		cmd, root, args string
		confirm         bool
	}{
		{"stackfork --instance sf-a-1 seed", "", "--instance sf-a-1 seed", false},
		{"stackfork up", "/w", "--root /w up", false},
		{"stackfork prepare --reassign-ports", "/w", "--root /w prepare --reassign-ports", false},
		{"stackfork --instance sf-a-1 seed --force", "", "--instance sf-a-1 seed --force", true},
		{"stackfork down --instance sf-a-1 --volumes", "/w", "down --instance sf-a-1 --volumes", true},
		{"stackfork prune --stopped", "", "prune --stopped --yes", true},
		{"stackfork gc", "/w", "gc --apply", true},
		{"git -C '/r' worktree remove '/r/.worktrees/a'", "", "-C /r worktree remove /r/.worktrees/a", true},
		{"docker rm sf-a-1-app-run-1", "", "rm sf-a-1-app-run-1", false},
	}
	for _, c := range cases {
		p, err := s.planRun(c.cmd, c.root, wt)
		if err != nil {
			t.Errorf("%q refused: %v", c.cmd, err)
			continue
		}
		if got := strings.Join(p.Args, " "); got != c.args || (p.Confirm != "") != c.confirm {
			t.Errorf("%q: args %q confirm %q", c.cmd, got, p.Confirm)
		}
	}
}

// A destructive command the person cancels in the dialog never runs.
func TestRunCancelledInDialogRunsNothing(t *testing.T) {
	old := Confirm
	defer func() { Confirm = old }()
	asked := ""
	Confirm = func(title, text string) (bool, error) { asked = text; return false, nil }
	srv, err := New(t.TempDir(), t.TempDir(), "test", 55127)
	if err != nil {
		t.Fatal(err)
	}
	req := strings.NewReader(`{"command":"stackfork --instance sf-x-0123456789ab seed --force"}`)
	rec := doRun(srv, req)
	if !strings.Contains(rec, "cancelled; nothing ran") || !strings.Contains(asked, "seed --force") {
		t.Fatalf("response %s, dialog %q", rec, asked)
	}
}

func doRun(srv *Server, body *strings.Reader) string {
	r := httptest.NewRequest("POST", "/api/run", body)
	r.Host = "127.0.0.1:55127"
	r.Header.Set("X-Stackfork-Token", srv.Token)
	r.Header.Set("Origin", "http://127.0.0.1:55127")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w.Body.String()
}

func TestPlanRunRefusesDirtyWorktreeBeforeAsking(t *testing.T) {
	old := worktreeClean
	defer func() { worktreeClean = old }()
	worktreeClean = func(string) bool { return false }
	wt := func(repo, path string) (bool, string) { return true, "" }
	if _, err := (&Server{}).planRun("git -C /r worktree remove /r/.worktrees/a", "", wt); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("got %v", err)
	}
}
