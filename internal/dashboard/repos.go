package dashboard

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

//go:embed templates/repos.html.tmpl templates/cleanup.html.tmpl
var reposTemplateSrc embed.FS

var reposFuncs = template.FuncMap{
	"entryCommand": entryCommand,
	"date": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.Format("2006-01-02")
	},
	// shq single-quotes a value for the copyable shell commands.
	"shq":        shq,
	"prompt":     repos.Prompt,
	"worktreeAt": repos.WorktreeDir,
	"isRunning":  func(state string) bool { return isRunningState(state) },

	"relPath":         relPath,
	"tilde":           tildePath,
	"ago":             ago,
	"stamp":           stamp,
	"repoChip":        repoChip,
	"stateChip":       stateChip,
	"aheadBehindText": aheadBehindText,
	"bar":             bar,
	"laneOf":          laneOf,
	"detached":        func(branch string) bool { return strings.HasPrefix(branch, "detached-") },
	"shortSha":        shortSha,
	"isLast":          func(i, n int) bool { return i == n-1 },
	"unixSec":         func(t time.Time) int64 { return t.Unix() },
	"folder":          func(r repos.Repo) string { return r.Group },
	"firstSetup":      firstSetup,
}

// relPath is a worktree's path as the Repos table shows it: the main checkout
// says so, a path inside the repo is relative to it (.worktrees/x), anything
// else is absolute with the home folder written as ~.
func relPath(repo, path string) string {
	if path == repo {
		return "main checkout"
	}
	if rel, err := filepath.Rel(repo, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return tildePath(path)
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + filepath.ToSlash(p[len(home):])
		}
	}
	return p
}

// ago is the relative LAST ACTIVITY text ("5 min ago", "3 w ago"); the zero
// time is an em dash.
func ago(t time.Time) string {
	if t.IsZero() {
		return "\u2014"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d w ago", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%d mo ago", int(d.Hours()/(24*30)))
	}
	return fmt.Sprintf("%d y ago", int(d.Hours()/(24*365)))
}

// stamp is the full timestamp for a title attribute.
func stamp(t time.Time) string {
	if t.IsZero() {
		return "no commits"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// repoStatus is the STATUS column: a state tone (ok, attention, off) and the
// word. Running beats everything; a repo that is not set up says so before
// it says Stopped, because Start is refused for it.
func repoStatus(r repos.Repo) (state, label string) {
	running, partial, any := 0, 0, false
	for _, w := range r.Worktrees {
		if w.Instance == nil {
			continue
		}
		any = true
		switch w.Instance.State {
		case inventory.StateRunning:
			running++
		case inventory.StatePartial:
			partial++
		}
	}
	switch {
	case running > 0:
		return "ok", fmt.Sprintf("%d running", running)
	case partial > 0:
		return "attention", fmt.Sprintf("%d partial", partial)
	case r.Readiness == repos.NeedsLearn || r.Readiness == repos.Unknown:
		return "off", "Not set up"
	case r.Readiness == repos.Draft:
		return "attention", "Setup unverified"
	case any:
		return "off", "Stopped"
	}
	return "ok", "Ready"
}

// repoChip is repoStatus as a StatusChip for the template.
func repoChip(r repos.Repo) chipView {
	state, label := repoStatus(r)
	c := chipView{Class: state, Glyph: statusGlyph(state, label), Label: label}
	// "2 running" hides instances that are only partly up; say so on hover.
	running, partial := 0, 0
	for _, w := range r.Worktrees {
		if w.Instance == nil {
			continue
		}
		switch w.Instance.State {
		case inventory.StateRunning:
			running++
		case inventory.StatePartial:
			partial++
		}
	}
	if running > 0 && partial > 0 {
		c.Title = fmt.Sprintf("%d running, %d partial", running, partial)
	}
	return c
}

func statusGlyph(state, label string) string {
	switch {
	case strings.HasSuffix(label, "partial"):
		return glyphPartial
	case state == "ok" && label == "Ready":
		return "\u2713"
	case state == "ok":
		return glyphOK
	case label == "Setup unverified":
		return glyphBusy
	}
	return glyphOff
}

// aheadBehindText is the screen-reader / title text of the AheadBehindBar.
// The counts are against the branch's upstream, never against main.
func aheadBehindText(ahead, behind int, upstream string) string {
	switch {
	case upstream == "":
		return "no upstream"
	case ahead == 0 && behind == 0:
		return "up to date"
	}
	return fmt.Sprintf("%d ahead, %d behind upstream", ahead, behind)
}

// barView is the two segment widths (px) of the 56 px AheadBehindBar.
type barView struct{ Ahead, Behind int }

// bar splits 56 px in proportion to ahead / (ahead + behind), with a 4 px
// minimum for a non-zero side.
func bar(ahead, behind int) barView {
	const total, min = 56, 4
	switch {
	case ahead <= 0 && behind <= 0:
		return barView{}
	case behind <= 0:
		return barView{Ahead: total}
	case ahead <= 0:
		return barView{Behind: total}
	}
	a := total * ahead / (ahead + behind)
	if a < min {
		a = min
	}
	if a > total-min {
		a = total - min
	}
	return barView{Ahead: a, Behind: total - a}
}

// laneOf is the 1-based lane colour of a worktree (its app's colour, same as the instance card),
// 0 when it has no instance.
func laneOf(r repos.Repo, w repos.Worktree) int {
	if w.Instance == nil {
		return 0
	}
	return stackColorIndex(r.Name) + 1
}

func shortSha(branch string) string {
	sha := strings.TrimPrefix(branch, "detached-")
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return sha
}

// sortRepos orders the table: repos with a running or partial instance first,
// then most recent activity, then name. The input is left as it is.
func sortRepos(in []repos.Repo) []repos.Repo {
	out := append([]repos.Repo(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Running > 0) != (b.Running > 0) {
			return a.Running > 0
		}
		if !a.LastCommit.Equal(b.LastCommit) {
			return a.LastCommit.After(b.LastCommit)
		}
		return a.Name < b.Name
	})
	return out
}

// firstSetup is the path of the first repo (in table order) that is not set
// up: the one row that gets the view's filled "Set up" button.
func firstSetup(rs []repoView) string {
	for _, r := range rs {
		if r.Readiness != repos.Ready {
			return r.Path
		}
	}
	return ""
}

var reposTemplate = template.Must(
	template.New("repos.html.tmpl").Funcs(reposFuncs).ParseFS(reposTemplateSrc, "templates/repos.html.tmpl", "templates/cleanup.html.tmpl"))

// StartFunc starts the instance of one checkout, writing progress lines to
// out. The CLI supplies it (cli.StartEnv), so the page and the command run
// the same code and the dashboard never imports the CLI. trigger is the
// label the run records (always TriggerDashboard here).
// obs receives the run's state changes for the stream (nil: none wanted).
type StartFunc func(out, errOut io.Writer, checkout, trigger string, obs func(runs.Event)) error

// ResetFunc throws away the environment of one checkout and starts it from
// zero (cli.StartEnv with Reset). current: go on with the code as it is when
// the checkout is behind and cannot be updated. fromMain: prepare with the
// main checkout's recipe, the worktree's own adapter files moved aside.
type ResetFunc func(out, errOut io.Writer, checkout string, current, fromMain bool, obs func(runs.Event)) error

// ResetPreviewFunc is what the confirmation shows: the text, and whether the
// checkout is behind its upstream and blocked from updating.
type ResetPreviewFunc func(checkout string, fromMain bool) (text string, blocked bool, err error)

// TriggerDashboard is the trigger of every run the dashboard starts, directly
// or through the commands of /api/run (STACKFORK_TRIGGER).
const TriggerDashboard = "dashboard"

// reposState is the in-memory scan the page and the actions work from.
type reposState struct {
	mu       sync.Mutex
	res      *repos.Result
	at       time.Time
	scanning bool
	bg       sync.WaitGroup // background scans in flight
}

// Wait blocks until background scans have finished (tests use it so a scan
// never outlives its temp directories).
func (s *Server) Wait() { s.repos.bg.Wait() }

func (s *Server) goScan(full bool) {
	s.repos.bg.Add(1)
	go func() {
		defer s.repos.bg.Done()
		s.scanNow(full)
	}()
}

// rescanAfter is how old the in-memory scan may get before a request kicks
// off a background refresh (git data is cached, so a refresh is cheap).
const rescanAfter = 5 * time.Second

func (s *Server) scanNow(full bool) repos.Result {
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
	}
	var insts []inventory.Instance
	if len(cfg.Roots) > 0 {
		if inv, err := s.collect(); err == nil {
			insts = inv.Instances
		}
	}
	res := repos.Scan(repos.Options{
		Config: cfg, Instances: insts, ProfileDirs: s.ProfileDirs,
		CacheFile: filepath.Join(s.NewStateBase, "repos-cache.json"), Rescan: full,
	})
	s.repos.mu.Lock()
	s.repos.res, s.repos.at, s.repos.scanning = &res, time.Now(), false
	s.repos.mu.Unlock()
	return res
}

// currentRepos returns the last scan at once and refreshes it in the
// background when it is old; the very first call has to scan.
func (s *Server) currentRepos() (res repos.Result, scanning bool) {
	s.repos.mu.Lock()
	cur := s.repos.res
	if cur != nil && !s.repos.scanning && time.Since(s.repos.at) > rescanAfter {
		s.repos.scanning = true
		s.goScan(false)
	}
	scanning = s.repos.scanning
	s.repos.mu.Unlock()
	if cur == nil {
		return s.scanNow(false), false
	}
	return *cur, scanning
}

// rescanAsync forces a refresh now (after a config change or an action).
func (s *Server) rescanAsync(full bool) {
	s.repos.mu.Lock()
	if s.repos.scanning {
		s.repos.mu.Unlock()
		return
	}
	s.repos.scanning = true
	s.repos.mu.Unlock()
	s.goScan(full)
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	res, scanning := s.currentRepos()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Stackfork-Scanning", strconv.FormatBool(scanning))
	cfg, _ := config.Load()
	w.Write(s.reposJSON(res, cfg)) //nolint:errcheck // client gone
}

type repoView struct {
	repos.Repo
	Search  string
	Recipe  recipeView
	Plan    *planView
	Entry   *entryView
	Secrets []secretView
	Live    liveView
}

type reposPage struct {
	repos.Result
	Rows       []repoView
	FirstSetup string
	Scanning   bool
	// CleanupModes are the options of the cleanup selector.
	CleanupModes []string
}

func (s *Server) handleReposFragment(w http.ResponseWriter, r *http.Request) {
	res, scanning := s.currentRepos()
	page := reposPage{Result: res, Scanning: scanning, CleanupModes: []string{config.CleanupOff, config.CleanupSuggest, config.CleanupAuto}}
	cfg, _ := config.Load()
	lives := s.liveViews(res.Repos, cfg)
	for _, repo := range sortRepos(res.Repos) {
		var b strings.Builder
		b.WriteString(repo.Group + " " + repo.Name)
		for _, br := range repo.Branches {
			b.WriteString(" " + br.Name)
		}
		for _, w := range repo.Worktrees {
			b.WriteString(" " + w.Branch)
		}
		page.Rows = append(page.Rows, repoView{Repo: repo, Search: strings.ToLower(b.String()), Recipe: s.repoRecipe(repo), Plan: s.repoPlan(repo, cfg), Entry: repoEntry(repo), Secrets: s.repoSecrets(repo, cfg), Live: lives[repo.Path]})
	}
	page.FirstSetup = firstSetup(page.Rows)
	s.renderFragment(w, "repos", page)
}

func (s *Server) handleSettingsFragment(w http.ResponseWriter, r *http.Request) {
	res, _ := s.currentRepos()
	path, _ := config.Path()
	s.renderFragment(w, "settings", struct {
		repos.Result
		ConfigPath string
	}{res, path})
}

func (s *Server) renderFragment(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := reposTemplate.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- POST /api/config ------------------------------------------------------

type configRequest struct {
	Op    string `json:"op"`
	Value string `json:"value"`
}

// handleConfig edits the config file with the same code the CLI uses
// (design.md D7a). Root validation happens in config.AddRoot.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	var req configRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	cfg, err := config.Load()
	if err != nil {
		writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
		return
	}
	msg := "saved"
	switch req.Op {
	case "add-root":
		var p string
		if p, err = cfg.AddRoot(req.Value); err == nil {
			msg = "added " + p
		}
	case "remove-root":
		err = cfg.RemoveRoot(req.Value)
	case "add-exclude":
		err = cfg.AddExclude(req.Value)
	case "remove-exclude":
		err = cfg.RemoveExclude(req.Value)
	case "set-max-depth":
		n, convErr := strconv.Atoi(strings.TrimSpace(req.Value))
		if convErr != nil {
			err = fmt.Errorf("max depth must be a number")
		} else {
			err = cfg.SetMaxDepth(n)
		}
	case "set-mode":
		// value is REPO=MODE; REPO must be a repo found by the scan
		i := strings.LastIndex(req.Value, "=")
		if i < 0 {
			err = errors.New("expected REPO=MODE")
			break
		}
		repo, mode := req.Value[:i], req.Value[i+1:]
		if s.scannedRepo(repo) == nil {
			writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a repo found by the scan under a configured folder"})
			return
		}
		if k, kerr := knowledge.Load(filepath.Join(repo, ".stackfork", "knowledge.json")); kerr == nil && !k.Plan.SupportsMode(mode) && (mode == knowledge.ModeContainer || mode == knowledge.ModeHybrid) {
			var sup []string
			for _, m := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
				if k.Plan.SupportsMode(m) {
					sup = append(sup, m)
				}
			}
			err = fmt.Errorf("mode %s is not supported by this plan; supported: %s", mode, strings.Join(sup, ", "))
			break
		}
		err = cfg.SetRepoMode(repo, mode)
	case "rescan":
		s.rescanAsync(true)
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "rescan started"})
		return
	default:
		http.Error(w, "unknown op", http.StatusBadRequest)
		return
	}
	if err == nil {
		err = cfg.Save()
	}
	if err != nil {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: err.Error()})
		return
	}
	s.rescanAsync(false)
	writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: msg})
}

// --- repo actions (start, worktree-add, fetch) ------------------------------

var repoActions = map[string]bool{"start": true, "reset": true, "worktree-add": true, "fetch": true}

// inConfiguredRoots re-checks, at action time, that the path is still under
// a configured root (a root may have been removed since the scan).
func inConfiguredRoots(path string) bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	for _, root := range cfg.ResolvedRoots() {
		if config.Inside(root, path) {
			return true
		}
	}
	return false
}

// handleRepoAction runs after the token and Origin checks. Everything the
// request names must come from the current scan (design.md D8): the repo by
// path, a worktree by path, a branch from that repo's branch list.
func (s *Server) handleRepoAction(w http.ResponseWriter, req actionRequest) {
	res, _ := s.currentRepos()
	var repo *repos.Repo
	for i := range res.Repos {
		if res.Repos[i].Path == req.Repo {
			repo = &res.Repos[i]
		}
	}
	if repo == nil || !inConfiguredRoots(repo.Path) {
		writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a repo found by the scan under a configured folder"})
		return
	}

	if req.FromMain && req.Action != "reset" {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: "from_main applies to reset only"})
		return
	}

	// What does the request act on?
	var checkout, branch string
	if req.Action != "fetch" {
		if req.Path != "" {
			for _, wt := range repo.Worktrees {
				if wt.Path == req.Path {
					checkout = wt.Path
					branch = wt.Branch
				}
			}
			if checkout == "" || (req.Action != "start" && req.Action != "reset") {
				writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a worktree of this repo"})
				return
			}
		} else {
			if err := repos.ValidBranch(req.Branch); err != nil {
				writeActionResult(w, http.StatusBadRequest, actionResponse{Message: err.Error()})
				return
			}
			found := false
			for _, b := range repo.Branches {
				found = found || b.Name == req.Branch
			}
			if !found {
				writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a branch without a worktree in this repo"})
				return
			}
			branch = req.Branch
		}
		if req.Action == "start" && repo.Readiness != repos.Ready {
			writeActionResult(w, http.StatusConflict, actionResponse{Message: fmt.Sprintf("%s is %s: %s. Use \"Learn with your agent\" instead", repo.Name, repo.Readiness, repo.ReadinessReason)})
			return
		}
		if req.Action == "start" && s.StartEnv == nil {
			writeActionResult(w, http.StatusServiceUnavailable, actionResponse{Message: "start is not available in this build"})
			return
		}
		if req.Action == "reset" && (s.ResetEnv == nil || s.ResetPreview == nil || checkout == "") {
			writeActionResult(w, http.StatusServiceUnavailable, actionResponse{Message: "reset is not available in this build"})
			return
		}
	}

	lockKey := repo.Path
	if checkout != "" {
		if p, err := identity.ProjectName(checkout, branch); err == nil {
			lockKey = p
		}
	}
	if !s.tryLockProject(lockKey) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "an action is already running for this instance"})
		return
	}
	defer s.unlockProject(lockKey)
	defer s.rescanAsync(false)

	switch req.Action {
	case "fetch":
		out, err := repos.Fetch(repo.Path)
		if err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: "fetch failed: " + lastLine(out, err)})
			return
		}
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "fetched " + repo.Name})
	case "worktree-add":
		wt, err := repos.AddWorktree(repo.Path, branch, "")
		if err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error()})
			return
		}
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "created " + wt.Path + copiedNote(wt)})
	case "start":
		s.streamStart(w, repo, checkout, branch)
	case "reset":
		s.streamReset(w, repo, checkout, req.FromMain)
	}
}

func copiedNote(wt repos.WorktreeResult) string {
	if len(wt.Copied) == 0 {
		return ""
	}
	return " (copied " + strings.Join(wt.Copied, ", ") + ")"
}

func lastLine(out string, err error) string {
	if out = strings.TrimSpace(out); out != "" {
		lines := strings.Split(out, "\n")
		return lines[len(lines)-1]
	}
	return err.Error()
}

// stream starts a newline-delimited JSON answer: {"line": "..."} for each
// progress line, then (from finish) one {"ok": bool, "message": "..."}.
func stream(w http.ResponseWriter, okMessage string) (*lineWriter, func(error)) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	lw := &lineWriter{w: w}
	return lw, func(err error) {
		lw.flushPartial()
		resp := actionResponse{OK: err == nil, Message: okMessage}
		resp.Run, resp.FailedStep = lw.runInfo()
		if err != nil {
			resp.Message = err.Error()
		}
		b, _ := json.Marshal(resp)
		w.Write(append(b, '\n')) //nolint:errcheck // client gone
		flush(w)
	}
}

// approveHostSteps asks for the plan's host and prepare steps nobody approved
// on this machine, before anything is created or run (design.md D9). verb is
// the dialog's first word (Start, Reset).
func (s *Server) approveHostSteps(lw io.Writer, repo *repos.Repo, checkout, verb string) error {
	// Same lookup as StartEnv: the checkout's own knowledge.json, then the main repo's.
	k, err := knowledge.LoadFirst(checkout, repo.Path)
	if err != nil || k == nil {
		return err
	}
	cfg, _ := config.Load()
	mode := knowledge.ModeContainer
	if cfg != nil {
		mode, _ = cfg.ModeForPlan(repo.Path, k.Plan)
	}
	if ok, _ := lifecycle.TrustedHere(s.NewStateBase, repo.Path, k.Plan, mode); ok {
		return nil
	}
	diff := lifecycle.ReadableDiff(s.NewStateBase, repo.Path, k.Plan)
	prompt, err := lifecycle.TrustPrompt(k.Plan, diff)
	if err != nil { // no dialog: steps that cannot be shown are not approved
		return err
	}
	text := verb + " " + repo.Name + "?\n\nIts plan runs host commands nobody approved on this machine.\n\n" + prompt
	approved, err := Confirm("stackfork", text)
	if err != nil || !approved {
		if err == nil {
			err = errors.New("cancelled; nothing ran")
		}
		return err
	}
	if _, err := lifecycle.TrustHere(s.NewStateBase, repo.Path, k.Plan); err != nil {
		return err
	}
	fmt.Fprintln(lw, "trusted the host steps on this machine")
	return nil
}

// streamReset confirms with a native dialog that names the volumes (and the
// blocked update), then streams the reset like a Start.
func (s *Server) streamReset(w http.ResponseWriter, repo *repos.Repo, checkout string, fromMain bool) {
	lw, finish := stream(w, "reset "+repo.Name)
	text, blocked, err := s.ResetPreview(checkout, fromMain)
	if err != nil {
		finish(err)
		return
	}
	approved, err := Confirm("stackfork", text)
	if err != nil || !approved {
		if err == nil {
			err = errors.New("cancelled; nothing was changed")
		}
		finish(err)
		return
	}
	if err := s.approveHostSteps(lw, repo, checkout, "Reset"); err != nil {
		finish(err)
		return
	}
	finish(s.ResetEnv(lw, lw, checkout, blocked, fromMain, lw.observe))
}

// streamStart answers with newline-delimited JSON: {"line": "..."} for each
// progress line as it happens, then one {"ok": bool, "message": "..."}.
func (s *Server) streamStart(w http.ResponseWriter, repo *repos.Repo, checkout, branch string) {
	lw, finish := stream(w, "started "+repo.Name)
	if err := s.approveHostSteps(lw, repo, checkout, "Start"); err != nil {
		finish(err)
		return
	}
	if checkout == "" { // a branch without a worktree: create it first
		fmt.Fprintf(lw, "creating a worktree for %s\n", branch)
		wt, err := repos.AddWorktree(repo.Path, branch, "")
		if err != nil {
			finish(err)
			return
		}
		fmt.Fprintf(lw, "created %s%s\n", wt.Path, copiedNote(wt))
		checkout = wt.Path
	}
	finish(s.StartEnv(lw, lw, checkout, TriggerDashboard, lw.observe))
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// lineWriter turns written text into one {"line": ...} JSON object per line,
// tagged with the step that is running. Observe feeds it the run's state
// changes, which it sends as {"run": ...} once and {"step": ...} per change
// (design.md D12). Lines reach it already redacted by the run's step writers.
type lineWriter struct {
	w   http.ResponseWriter
	mu  sync.Mutex
	buf []byte

	step     string // running step: tags the lines; "" between steps
	run      int
	project  string
	steps    []streamStep
	announce bool
	failed   string // first failed step
}

type streamStep struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.emit(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
	return len(p), nil
}

func (l *lineWriter) flushPartial() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
}

func (l *lineWriter) flushLocked() {
	if len(l.buf) > 0 {
		l.emit(string(l.buf))
		l.buf = nil
	}
}

func (l *lineWriter) emit(line string) {
	if line = strings.TrimRight(line, "\r"); strings.TrimSpace(line) == "" {
		return
	}
	o := map[string]string{"line": line}
	if l.step != "" {
		o["step"] = l.step
	}
	l.send(o)
}

func (l *lineWriter) send(o any) {
	b, _ := json.Marshal(o)
	l.w.Write(append(b, '\n')) //nolint:errcheck // client gone
	flush(l.w)
}

// runInfo is the run id and the first failed step, for the final object.
func (l *lineWriter) runInfo() (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.run, l.failed
}

// observe turns one run event into stream objects. Pending events only
// collect the step list; the run object goes out before the first step
// moves, so it names every step of the run.
func (l *lineWriter) observe(ev runs.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.run, l.project = ev.Run, ev.Project
	if ev.Step == "" {
		return
	}
	if ev.State == runs.Pending {
		l.steps = append(l.steps, streamStep{ID: ev.Step, Kind: ev.Kind, State: runs.Pending})
		return
	}
	l.flushLocked() // output so far belongs to the previous step
	if !l.announce {
		l.announce = true
		l.send(map[string]any{"run": map[string]any{"id": l.run, "project": l.project, "steps": l.steps}})
	}
	step := map[string]string{"id": ev.Step, "state": ev.State}
	if ev.Reason != "" {
		step["reason"] = ev.Reason
	}
	l.send(map[string]any{"step": step})
	l.step = ""
	if ev.State == runs.StepRunning {
		l.step = ev.Step
	}
	if ev.State == runs.Failed && l.failed == "" {
		l.failed = ev.Step
	}
}
