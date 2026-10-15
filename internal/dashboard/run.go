package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/guard"
	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// runRequest is POST /api/run's body: a command the page shows (the same
// text its Copy button copies) and, for a command on an instance card, the
// instance it belongs to.
type runRequest struct {
	Command string `json:"command"`
	Project string `json:"project,omitempty"`
}

// runPlan is a parsed, allowed command: the argv to execute and whether a
// person has to confirm it first.
type runPlan struct {
	Name    string // "stackfork", "git" or "docker"
	Args    []string
	Confirm string // non-empty: the question the native dialog asks
	DryRun  []string
	Down    string // after a worktree removal: this instance's stack goes too
}

// runStackfork are the stackfork actions the page may run. Everything else
// (config, learn record, hooks, ...) stays copy-only.
var runStackfork = map[string]bool{
	"up": true, "down": true, "restart": true, "start": true, "stop": true,
	"seed": true, "gc": true, "prune": true, "prepare": true, "learn": true, "proxy": true,
}

// Confirm asks the person at this Mac to approve a destructive command with
// a native dialog. An agent can forge the HTTP request, not click the
// dialog. A var so tests can answer it.
var Confirm = func(title, text string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, errors.New("no confirmation dialog on this system; run it in a terminal")
	}
	const script = `on run argv
display dialog (item 2 of argv) with title (item 1 of argv) buttons {"Cancel", "Run"} default button "Cancel" cancel button "Cancel" with icon caution giving up after 120
return button returned of result
end run`
	out, err := exec.Command("osascript", "-e", script, title, text).Output()
	if err != nil {
		return false, nil // Cancel exits non-zero
	}
	return strings.TrimSpace(string(out)) == "Run", nil
}

// planRun allowlists cmd. root is the card's instance root ("" off a card);
// worktree reports whether REPO/PATH is a scanned, non-main worktree and its
// instance.
func (s *Server) planRun(cmd, root string, worktree func(repo, path string) (ok bool, project string)) (*runPlan, error) {
	segs := guard.Segments(cmd, "")
	if len(segs) != 1 || strings.ContainsAny(cmd, ";&|`$<>\n") {
		return nil, errors.New("only a single command, with no shell syntax, can run from the page")
	}
	w := segs[0].Words
	switch w[0] {
	case "stackfork":
		return s.planStackfork(w[1:], root)
	case "git":
		// git -C REPO worktree remove PATH: never --force, git itself
		// refuses a worktree with changes.
		if len(w) != 6 || w[1] != "-C" || w[3] != "worktree" || w[4] != "remove" {
			return nil, errors.New("the only git command the page runs is `git -C REPO worktree remove PATH`")
		}
		ok, project := worktree(w[2], w[5])
		if !ok {
			return nil, errors.New("not a worktree (other than the main checkout) of a scanned repo")
		}
		p := &runPlan{Name: "git", Args: w[1:], Down: project,
			Confirm: "Remove the worktree " + w[5] + "?\n\ngit refuses if it has uncommitted changes."}
		if project != "" {
			p.Confirm += "\n\nIts stack " + project + " is removed too, including its database volumes."
		}
		return p, nil
	case "docker":
		if len(w) < 3 || w[1] != "rm" {
			return nil, errors.New("the only docker command the page runs is `docker rm` of stackfork's one-off containers")
		}
		for _, c := range w[2:] {
			if !strings.HasPrefix(c, identity.Prefix+"-") && !strings.HasPrefix(c, identity.LegacyPrefix+"-") {
				return nil, fmt.Errorf("%s is not a stackfork container", c)
			}
		}
		return &runPlan{Name: "docker", Args: w[1:]}, nil
	}
	return nil, errors.New("not a command the page runs")
}

func (s *Server) planStackfork(args []string, root string) (*runPlan, error) {
	var global, rest []string
	action := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if action == "" && (a == "--instance" || a == "--root") && i+1 < len(args) {
			global = append(global, a, args[i+1])
			i++
			continue
		}
		if action == "" {
			if strings.HasPrefix(a, "-") {
				return nil, fmt.Errorf("flag %s is not allowed from the page", a)
			}
			action = a
			continue
		}
		rest = append(rest, a)
	}
	if !runStackfork[action] {
		return nil, fmt.Errorf("stackfork %s runs only in a terminal", action)
	}
	has := func(names ...string) bool {
		for _, r := range rest {
			for _, n := range names {
				if r == n {
					return true
				}
			}
		}
		return false
	}
	switch action {
	case "prepare":
		if len(rest) != 1 || rest[0] != "--reassign-ports" {
			return nil, errors.New("only `prepare --reassign-ports` runs from the page")
		}
	case "learn":
		if len(rest) != 0 {
			return nil, errors.New("only a plain `stackfork learn` runs from the page")
		}
	case "proxy":
		if len(rest) != 1 || (rest[0] != "up" && rest[0] != "reload") {
			return nil, errors.New("only `proxy up` and `proxy reload` run from the page")
		}
	}
	machineWide := action == "gc" || action == "prune" || action == "proxy"
	if len(global) == 0 && root != "" && !machineWide {
		global = []string{"--root", root}
	}
	p := &runPlan{Name: "stackfork"}
	argv := append(append(append([]string{}, global...), action), rest...)
	// prune and gc show a dry run by default; the button means "do it",
	// and the dialog shows that dry run.
	switch {
	case action == "prune" && !has("--yes", "-y"):
		p.DryRun = argv
		argv = append(argv, "--yes")
	case action == "gc" && !has("--apply"):
		p.DryRun = argv
		argv = append(argv, "--apply")
	}
	full := "stackfork " + strings.Join(argv, " ")
	if guard.Destructive(full, "") || (action == "down" && has("--volumes", "-v")) {
		p.Confirm = "Run this?\n\n" + full
		if action == "down" {
			p.Confirm += "\n\nThis deletes the stack's volumes: its database is lost."
		}
	}
	p.Args = argv
	return p, nil
}

func (s *Server) execPlan(ctx context.Context, name string, args []string) (string, error) {
	bin := name
	if name == "stackfork" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		bin = exe
		args = append([]string{"--state-dir", s.NewStateBase, "--legacy-state-dir", s.LegacyStateBase}, args...)
	}
	var out bytes.Buffer
	c := exec.CommandContext(ctx, bin, args...)
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	return strings.TrimSpace(out.String()), err
}

// handleRun is POST /api/run: behind the token and Origin checks like every
// action, one run at a time, output returned to the page's log.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	root := ""
	if req.Project != "" {
		_, rt, _, _, err := s.resolveInstance(req.Project)
		if err != nil {
			writeActionResult(w, http.StatusBadRequest, actionResponse{Message: err.Error()})
			return
		}
		root = rt
	}
	plan, err := s.planRun(req.Command, root, s.scannedWorktree)
	if err != nil {
		writeActionResult(w, http.StatusForbidden, actionResponse{Message: err.Error()})
		return
	}
	const lockKey = "\x00run"
	if !s.tryLockProject(lockKey) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "another command from the page is still running"})
		return
	}
	defer s.unlockProject(lockKey)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	if plan.Confirm != "" {
		text := plan.Confirm
		if plan.DryRun != nil {
			dry, _ := s.execPlan(ctx, "stackfork", plan.DryRun)
			if len(dry) > 1500 {
				dry = dry[:1500] + "\n..."
			}
			text += "\n\nWhat it removes:\n" + dry
		}
		ok, err := Confirm("stackfork", text)
		if err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error()})
			return
		}
		if !ok {
			writeActionResult(w, http.StatusOK, actionResponse{Message: "cancelled; nothing ran"})
			return
		}
	}
	out, err := s.execPlan(ctx, plan.Name, plan.Args)
	if err == nil && plan.Down != "" {
		more, derr := s.execPlan(ctx, "stackfork", []string{"--instance", plan.Down, "down", "--volumes"})
		out, err = strings.TrimSpace(out+"\n"+more), derr
	}
	if out == "" {
		out = "done"
	}
	writeActionResult(w, http.StatusOK, actionResponse{OK: err == nil, Message: out})
}

// scannedWorktree reports whether path is a non-main worktree of the
// scanned repo at repo, and the instance it runs, if any.
func (s *Server) scannedWorktree(repo, path string) (bool, string) {
	res, _ := s.currentRepos()
	for _, r := range res.Repos {
		if r.Path != repo || !inConfiguredRoots(r.Path) {
			continue
		}
		for _, wt := range r.Worktrees {
			if wt.Path == path && !wt.Main {
				project := ""
				if wt.Instance != nil {
					project = wt.Instance.Project
				}
				return true, project
			}
		}
	}
	return false, ""
}
