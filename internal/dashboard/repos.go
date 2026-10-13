package dashboard

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

//go:embed templates/repos.html.tmpl
var reposTemplateSrc embed.FS

var reposFuncs = template.FuncMap{
	"date": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.Format("2006-01-02")
	},
	// shq single-quotes a value for the copyable shell commands.
	"shq":        func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" },
	"prompt":     repos.Prompt,
	"worktreeAt": repos.WorktreeDir,
	"isRunning":  func(state string) bool { return isRunningState(state) },
}

var reposTemplate = template.Must(
	template.New("repos.html.tmpl").Funcs(reposFuncs).ParseFS(reposTemplateSrc, "templates/repos.html.tmpl"))

// StartFunc starts the instance of one checkout, writing progress lines to
// out. The CLI supplies it (cli.StartEnv), so the page and the command run
// the same code and the dashboard never imports the CLI.
type StartFunc func(out, errOut io.Writer, checkout string) error

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
	w.Write(repos.Marshal(res)) //nolint:errcheck // client gone
}

type repoGroup struct {
	Name  string
	Repos []repoView
}

type repoView struct {
	repos.Repo
	Search string
	Recipe recipeView
}

type reposPage struct {
	repos.Result
	Groups   []repoGroup
	Scanning bool
}

func (s *Server) handleReposFragment(w http.ResponseWriter, r *http.Request) {
	res, scanning := s.currentRepos()
	page := reposPage{Result: res, Scanning: scanning}
	for _, repo := range res.Repos {
		if n := len(page.Groups); n == 0 || page.Groups[n-1].Name != repo.Group {
			page.Groups = append(page.Groups, repoGroup{Name: repo.Group})
		}
		g := &page.Groups[len(page.Groups)-1]
		g.Repos = append(g.Repos, repoView{Repo: repo, Search: strings.ToLower(repo.Group + " " + repo.Name), Recipe: s.repoRecipe(repo)})
	}
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

var repoActions = map[string]bool{"start": true, "worktree-add": true, "fetch": true}

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
			if checkout == "" || req.Action != "start" {
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

// streamStart answers with newline-delimited JSON: {"line": "..."} for each
// progress line as it happens, then one {"ok": bool, "message": "..."}.
func (s *Server) streamStart(w http.ResponseWriter, repo *repos.Repo, checkout, branch string) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	lw := &lineWriter{w: w}
	finish := func(err error) {
		lw.flushPartial()
		resp := actionResponse{OK: err == nil, Message: "started " + repo.Name}
		if err != nil {
			resp.Message = err.Error()
		}
		b, _ := json.Marshal(resp)
		w.Write(append(b, '\n')) //nolint:errcheck // client gone
		flush(w)
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
	finish(s.StartEnv(lw, lw, checkout))
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// lineWriter turns written text into one {"line": ...} JSON object per line.
type lineWriter struct {
	w   http.ResponseWriter
	mu  sync.Mutex
	buf []byte
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
	if len(l.buf) > 0 {
		l.emit(string(l.buf))
		l.buf = nil
	}
}

func (l *lineWriter) emit(line string) {
	if line = strings.TrimRight(line, "\r"); strings.TrimSpace(line) == "" {
		return
	}
	b, _ := json.Marshal(map[string]string{"line": line})
	l.w.Write(append(b, '\n')) //nolint:errcheck // client gone
	flush(l.w)
}
