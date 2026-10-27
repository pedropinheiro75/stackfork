package dashboard

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// actionRequest is POST /api/actions' body (design.md D2).
type actionRequest struct {
	Action  string `json:"action"`
	Project string `json:"project"`
	Service string `json:"service,omitempty"`

	// Repo browser actions (start, worktree-add, fetch): the repo's path,
	// then an existing worktree's path or a branch without a worktree.
	Repo   string `json:"repo,omitempty"`
	Path   string `json:"path,omitempty"`
	Branch string `json:"branch,omitempty"`
	// FromMain: reset rebuilds the worktree from the main checkout's recipe.
	FromMain bool `json:"from_main,omitempty"`

	// trigger labels the run an "up" records; "" is the dashboard. "resume" also
	// takes the plain up path (no re-prepare, no plan steps) even for a hybrid plan.
	trigger string
}

type actionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	// Run and FailedStep end a streamed Start (design.md D12).
	Run        int    `json:"run,omitempty"`
	FailedStep string `json:"failed_step,omitempty"`
	// Command is what a person runs in a terminal when the page cannot
	// (no native dialog): the page shows it to copy.
	Command string `json:"command,omitempty"`
}

// validActions is design.md D4's whole action set. Nothing outside this
// set is ever dispatched, and down here never has a way to request
// --volumes - lifecycle.Options{} always leaves Volumes false.
var validActions = map[string]bool{
	"up": true, "down": true, "restart": true,
	"service-start": true, "service-stop": true, "host-remove": true,
	"proxy-up": true, "proxy-down": true, "proxy-reload": true,
	"free-space": true,
}

// proxyLockKey is the mutex key for the three proxy-* actions, which are
// not scoped to one project.
const proxyLockKey = "\x00proxy"

// handleActions is the D5 security boundary (token, then Origin) plus the
// D4 action dispatch: up, down (no volumes), restart, service-start/
// service-stop, proxy-up/down/reload, each running through the exact
// function the matching CLI subcommand calls (internal/lifecycle), one at
// a time per project (task 4.2).
func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if repoActions[req.Action] {
		s.handleRepoAction(w, req)
		return
	}
	if !validActions[req.Action] {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	isProxyAction := strings.HasPrefix(req.Action, "proxy-") || req.Action == "free-space"
	if !isProxyAction && req.Project == "" {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: "project is required"})
		return
	}

	lockKey := req.Project
	if isProxyAction {
		lockKey = proxyLockKey
	}
	if !s.tryLockProject(lockKey) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "an action is already running for this instance"})
		return
	}
	defer s.unlockProject(lockKey)

	writeActionResult(w, http.StatusOK, s.runAction(req))
}

// runAction dispatches one already-locked, already-validated action.
func (s *Server) runAction(req actionRequest) actionResponse {
	switch req.Action {
	case "up", "down", "restart", "service-start", "service-stop", "host-remove":
		instanceDir, root, branch, info, err := s.resolveInstance(req.Project)
		if err != nil {
			return actionResponse{Message: err.Error()}
		}
		name, isHost := hostName(info, req.Service)
		if isHost && req.Action != "up" && req.Action != "down" {
			return s.hostAction(req, name, instanceDir, root)
		}
		if req.Action == "host-remove" {
			return actionResponse{Message: req.Service + " is not a host app"}
		}
		var out bytes.Buffer
		var cascaded []string
		opts := lifecycle.Options{Cascaded: &cascaded, LockWant: true}
		// service-start/service-stop (fix 5) are distinct action names
		// from the stack-level up/down since they act on one container,
		// not the whole instance; they run through lifecycle.Execute's
		// "start"/"stop" actions, the same ones `stackfork start SERVICE`/
		// `stackfork stop SERVICE` use.
		lifecycleAction := req.Action
		switch req.Action {
		case "restart":
			opts.Service = req.Service
		case "service-start":
			lifecycleAction, opts.Service = "start", req.Service
		case "service-stop":
			lifecycleAction, opts.Service = "stop", req.Service
		}
		if req.Action == "up" && req.trigger == "" && s.StartEnv != nil && planRunsHost(root) {
			// lifecycle.Execute only knows compose; a hybrid up runs the plan.
			// StartEnv starts the checkout's current branch: it must be this card's.
			if cur, _ := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output(); strings.TrimSpace(string(cur)) != branch {
				return actionResponse{Message: fmt.Sprintf("%s is now on %s, not %s; switch back or start it from Repos", root, strings.TrimSpace(string(cur)), branch)}
			}
			if err := s.StartEnv(&out, &out, root, TriggerDashboard, nil); err != nil {
				return actionResponse{Message: err.Error()}
			}
			return actionResponse{OK: true, Message: "up complete"}
		}
		main := repos.MainOrSelf(root)
		compose := func(so, se io.Writer) (int, error) {
			return lifecycle.Execute(so, se, lifecycleAction, root, branch, req.Project, instanceDir,
				s.NewStateBase, s.LegacyStateBase, opts)
		}
		var code int
		if lifecycleAction == "up" { // a failed up leaves seeded:false for the next up
			mode := lifecycle.PlanMode(root, main)
			resume := req.trigger == TriggerResume
			if resume { // what the mode starts: a host app's replaced service stays down
				if k, _, kerr := knowledge.LoadFor(root, main); kerr == nil && k != nil {
					if names, all, serr := lifecycle.StartedServices(instanceDir, k.Plan, mode); serr == nil && !all {
						opts.Services = names
					}
				}
			}
			rec, berr := runs.Begin(instanceDir, runs.Meta{Command: "up", Trigger: cmp.Or(req.trigger, TriggerDashboard), Mode: mode, Project: req.Project,
				Branch: branch, Out: &out, Secrets: func() []redact.Secret { return opts.Secrets.Redactions() }})
			if berr != nil { // another run owns the instance: nothing runs
				return actionResponse{Message: berr.Error()}
			}
			rec.SetRetry("stackfork --instance " + req.Project + " up")
			if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); resume && m != nil && m.SeedPending {
				rec.Step("seed", knowledge.KindExec, "seed").Skip("seed pending; run Start or stackfork seed")
			}
			var perr error
			if opts.Secrets, perr = lifecycle.InstancePreflight(rec, &out, root, instanceDir, true); perr != nil {
				rec.Finish(perr)
				return actionResponse{Message: out.String() + perr.Error()}
			}
			res := lifecycle.UpAndSeed(&out, &out, lifecycle.UpSeed{
				Run: rec, Root: root, Main: main, Branch: branch, Project: req.Project, InstanceDir: instanceDir,
				NewBase: s.NewStateBase, LegacyBase: s.LegacyStateBase, Mode: mode,
				Plan: lifecycle.LoadPlan(root, main), Seed: !resume, Compose: compose,
			})
			runErr := res.RunError(rec, req.Project)
			if res.Err == nil && res.Code == 0 && runErr == nil {
				base := []string{"--root", root, "--state-dir", s.NewStateBase, "--legacy-state-dir", s.LegacyStateBase}
				if herr := lifecycle.StartHostApps(rec, &out, base, root, main, instanceDir, s.NewStateBase, s.LegacyStateBase, mode, lifecycle.LoadPlan(root, main)); herr != nil {
					runErr, res.Err = herr, herr
				}
			}
			rec.Finish(runErr)
			code, err = res.Code, res.Err
		} else {
			code, err = compose(&out, &out)
		}
		resp := execResult(lifecycleAction, code, err, out.String())
		if resp.OK && len(cascaded) > 0 { // a cascading stop says what else it stopped
			resp.Message = fmt.Sprintf("stopped %s and its dependents: %s", req.Service, strings.Join(cascaded, ", "))
		}
		return resp
	case "proxy-up":
		port, err := proxy.ReadPort(s.NewStateBase)
		if err != nil {
			return actionResponse{Message: err.Error()}
		}
		info, err := lifecycle.ProxyUp(s.NewStateBase, s.LegacyStateBase, port)
		if err != nil {
			return actionResponse{Message: err.Error()}
		}
		return actionResponse{OK: true, Message: fmt.Sprintf("proxy running on port %d", info.Port)}
	case "proxy-down":
		if _, err := lifecycle.ProxyDown(); err != nil {
			return actionResponse{Message: err.Error()}
		}
		return actionResponse{OK: true, Message: "proxy stopped"}
	case "proxy-reload":
		if err := lifecycle.ProxyReload(s.NewStateBase, s.LegacyStateBase); err != nil {
			return actionResponse{Message: err.Error()}
		}
		return actionResponse{OK: true, Message: "proxy reloaded"}
	case "free-space":
		return s.freeSpace()
	default:
		return actionResponse{Message: "unknown action"}
	}
}

// planRunsHost reports whether the checkout's plan, in this machine's mode
// for the repo, runs a host command: a host step or an exec step on the host.
func planRunsHost(root string) bool {
	main := repos.MainOrSelf(root)
	k, _, err := knowledge.LoadFor(root, main)
	if err != nil || k == nil {
		return false
	}
	mode := lifecycle.PlanMode(root, main) // the mode the run itself uses (same main, so the same answer)
	run, _ := k.Plan.ForMode(mode)
	for _, st := range run {
		if st.Kind == knowledge.KindHost || st.IsHostExec() {
			return true
		}
	}
	return false
}

// hostName reports whether service is a host process row ("host:NAME" with
// NAME recorded in HostProcs). Plain names always belong to compose.
func hostName(info *state.Manifest, service string) (string, bool) {
	name, ok := strings.CutPrefix(service, "host:")
	if !ok {
		return "", false
	}
	_, ok = info.HostProcs[name]
	return name, ok
}

// hostAction runs service-stop, service-start or restart on a host process
// row: the host commands, not docker. Start of an exited row is a restart
// from the recorded command line.
func (s *Server) hostAction(req actionRequest, name, instanceDir, root string) actionResponse {
	var errBuf bytes.Buffer
	var err error
	switch req.Action {
	case "service-stop":
		var dependents []string
		dependents, err = lifecycle.StopHostCascadeLocked(&errBuf, root, instanceDir, name, s.NewStateBase, s.LegacyStateBase)
		if err == nil && len(dependents) > 0 {
			return actionResponse{OK: true, Message: fmt.Sprintf("stopped %s and its dependents: %s", name, strings.Join(dependents, ", "))}
		}
	case "host-remove":
		err = lifecycle.ForgetHostLocked(&errBuf, instanceDir, name, s.NewStateBase, s.LegacyStateBase)
	default:
		err = lifecycle.HostRestart(&errBuf, root, instanceDir, name, s.NewStateBase, s.LegacyStateBase)
	}
	if err != nil {
		return actionResponse{Message: err.Error()}
	}
	return actionResponse{OK: true, Message: req.Action + " complete"}
}

// execResult turns a lifecycle.Execute outcome into the response the page
// shows: a Go error (a refused guard, e.g. the branch-switch or busy-port
// checks) surfaces its own text unchanged, a nonzero docker exit code
// surfaces the command's own combined output, and success gets a short
// confirmation.
func execResult(action string, code int, err error, output string) actionResponse {
	if err != nil {
		return actionResponse{Message: err.Error()}
	}
	if code != 0 {
		msg := strings.TrimSpace(output)
		if msg == "" {
			msg = fmt.Sprintf("%s exited with status %d", action, code)
		}
		return actionResponse{Message: msg}
	}
	return actionResponse{OK: true, Message: action + " complete"}
}

func writeActionResult(w http.ResponseWriter, status int, resp actionResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck // response already committed; nothing to do with a write error here
}

// tokenValid checks the X-Stackfork-Token header against the per-process
// token (design.md D5).
func (s *Server) tokenValid(r *http.Request) bool {
	token := r.Header.Get("X-Stackfork-Token")
	return token != "" && token == s.Token
}

// tryLockProject and unlockProject implement design.md D4's "actions run
// one at a time per instance"; a second request while one is running for
// the same key gets 409.
func (s *Server) tryLockProject(key string) bool {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	if s.actionsRun[key] {
		return false
	}
	s.actionsRun[key] = true
	return true
}

// Running counts the actions in flight (start, up, run...), so a shutdown
// can wait for them instead of killing a start halfway.
func (s *Server) Running() int {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	return len(s.actionsRun)
}

// ActionLocked reports whether an action runs for the instance now; the live
// loop skips a reconcile then (add-live-mode D9).
func (s *Server) ActionLocked(project string) bool {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	return s.actionsRun[project]
}

func (s *Server) unlockProject(key string) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	delete(s.actionsRun, key)
}

// resolveInstance finds an instance's directory, root and branch from its
// project name, in whichever state directory its prefix names (task
// 4.2/4.3). Every dashboard-triggered action and log request goes through
// this, so a project name is always resolved to a real, on-disk manifest
// before any docker command runs.
func (s *Server) resolveInstance(project string) (instanceDir, root, branch string, info *state.Manifest, err error) {
	if !identity.InstancePattern.MatchString(project) {
		return "", "", "", nil, fmt.Errorf("invalid project name %q", project)
	}
	base := s.NewStateBase
	if identity.IsLegacyInstance(project) {
		base = s.LegacyStateBase
	}
	instanceDir = filepath.Join(base, project)
	info, err = state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return "", "", "", nil, err
	}
	if info == nil {
		return "", "", "", nil, fmt.Errorf("no such instance %q", project)
	}
	return instanceDir, info.Root, info.Branch, info, nil
}

// handleLogs streams a service's logs (task 4.3): `?project=&service=&
// tail=200&follow=1`, redacted the same way `stackfork logs` is. Follow
// mode ends when the client disconnects (the request's own context, which
// exec.CommandContext ties the docker process to), and at most
// maxLogStreams run at once - past that, 429.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if project == "" {
		http.Error(w, "project is required", http.StatusBadRequest)
		return
	}
	service := r.URL.Query().Get("service")
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "200"
	}
	if n, err := strconv.Atoi(tail); err != nil || n < 0 {
		http.Error(w, "invalid tail", http.StatusBadRequest)
		return
	}
	follow := r.URL.Query().Get("follow") == "1"

	instanceDir, root, _, info, err := s.resolveInstance(project)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if name, ok := hostName(info, service); ok {
		// Snapshot only: a host log is a plain file, so follow=1 gets the
		// same snapshot plus a closing note instead of a live stream.
		n, _ := strconv.Atoi(tail)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		secrets, serr := lifecycle.LogSecrets(instanceDir, info)
		if serr != nil {
			needSecretCheck(w, serr)
			return
		}
		redacted := redact.NewWriter(w, secrets)
		redacted.Write(lastLines(filepath.Join(instanceDir, "host-"+name+".log"), n)) //nolint:errcheck // client gone
		if follow {
			redacted.Write([]byte("(live follow is not available for host apps; reopen to refresh)\n")) //nolint:errcheck // client gone
		}
		redacted.Close()
		return
	}

	select {
	case s.logSlots <- struct{}{}:
		defer func() { <-s.logSlots }()
	default:
		http.Error(w, "too many concurrent log streams", http.StatusTooManyRequests)
		return
	}

	configPath := filepath.Join(instanceDir, "compose.json")
	args := []string{"compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath, "--profile", "*",
		"logs", "--tail", tail}
	if follow {
		args = append(args, "--follow")
	}
	if service != "" {
		args = append(args, service)
	}

	secrets, serr := lifecycle.LogSecrets(instanceDir, info)
	if serr != nil {
		needSecretCheck(w, serr)
		return
	}
	cmd := exec.CommandContext(r.Context(), "docker", args...)
	cmd.Env = compose.DockerEnv(nil)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	redacted := redact.NewWriter(flushWriter{w}, secrets)
	cmd.Stdout = redacted
	cmd.Stderr = redacted
	cmd.Run() //nolint:errcheck // the client sees whatever reached the stream; a nonzero exit isn't reported separately
	redacted.Close()
}

// needSecretCheck answers a log request that cannot be redacted without a
// prompt (design D11): no log is shown until the person starts a check.
func needSecretCheck(w http.ResponseWriter, why error) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Stackfork-Needs", "secret-check")
	w.WriteHeader(http.StatusConflict)
	fmt.Fprintln(w, why.Error()) // the gate sentence, then one line per reason
}

// flushWriter flushes the underlying ResponseWriter after every write, so
// a follow-mode log stream reaches the browser as it happens rather than
// buffering until the response ends.
type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if flusher, ok := f.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

// hostLogWindow bounds how much of a host log is read: a dev server can log
// for days.
const hostLogWindow = 256 << 10

// lastLines returns the last n lines of a file, reading only its final
// hostLogWindow bytes (nil when it cannot be read).
func lastLines(path string, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	off := max(0, fi.Size()-hostLogWindow)
	b := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
		return nil
	}
	if off > 0 { // the window starts mid-line: drop that partial line
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		} else {
			return nil
		}
	}
	lines := bytes.SplitAfter(b, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	return bytes.Join(lines, nil)
}
