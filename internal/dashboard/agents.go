package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// agentDef is a coding agent CLI the Learn modal can open in a terminal
// with the learn prompt as its first message.
type agentDef struct {
	ID, Name, Bin string
	Flag          string // before the prompt ("" = positional)
}

var learnAgents = []agentDef{
	{ID: "claude", Name: "Claude Code", Bin: "claude"},
	{ID: "codex", Name: "Codex", Bin: "codex"},
	{ID: "copilot", Name: "Copilot CLI", Bin: "copilot", Flag: "-i"},
}

// LookPath finds an agent's binary; a var so tests control what is
// "installed".
var LookPath = exec.LookPath

type agentChoice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// scannedRepo returns the repo the scan found at path, under a configured
// root, or nil.
func (s *Server) scannedRepo(path string) *repos.Repo {
	res, _ := s.currentRepos()
	for i := range res.Repos {
		if res.Repos[i].Path == path && inConfiguredRoots(path) {
			return &res.Repos[i]
		}
	}
	return nil
}

func agentChoices(cfg *config.Config, repo string) []agentChoice {
	var out []agentChoice
	for _, a := range learnAgents {
		c := agentChoice{ID: a.ID, Name: a.Name, Available: true}
		if denied, reason := cfg.AgentDenied(a.ID, repo); denied {
			c.Available, c.Reason = false, reason
		} else if _, err := LookPath(a.Bin); err != nil {
			c.Available, c.Reason = false, "not installed"
		}
		out = append(out, c)
	}
	return out
}

// terminalApp picks the terminal: the "terminal" config key, else the one
// the dashboard was started from, else Terminal.app.
func terminalApp(cfg *config.Config) string {
	if t := cfg.Terminal(); t != "" {
		return t
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "WarpTerminal":
		return "warp"
	case "iTerm.app":
		return "iterm"
	}
	return "terminal"
}

// handleAgents is GET /api/agents?repo=PATH: the Learn modal's agents
// (installed, allowed for that folder) and the prompt.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	repo := s.scannedRepo(r.URL.Query().Get("repo"))
	if repo == nil {
		http.Error(w, "not a repo found by the scan", http.StatusNotFound)
		return
	}
	cfg, err := config.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"prompt":   repos.Prompt(repo.Name, repo.Path, repo.DefaultBranch),
		"agents":   agentChoices(cfg, repo.Path),
		"terminal": terminalApp(cfg),
		"canOpen":  runtime.GOOS == "darwin" || runtime.GOOS == "windows",
	})
}

// handleLearn is POST /api/learn {repo, agent}: opens a terminal window in
// the repo running the agent with the learn prompt.
func (s *Server) handleLearn(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	var req struct{ Repo, Agent string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	repo := s.scannedRepo(req.Repo)
	if repo == nil {
		writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a repo found by the scan under a configured folder"})
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
		return
	}
	var agent *agentDef
	for i := range learnAgents {
		if learnAgents[i].ID == req.Agent {
			agent = &learnAgents[i]
		}
	}
	if agent == nil {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: "unknown agent"})
		return
	}
	for _, c := range agentChoices(cfg, repo.Path) {
		if c.ID == agent.ID && !c.Available {
			writeActionResult(w, http.StatusForbidden, actionResponse{Message: agent.Name + ": " + c.Reason})
			return
		}
	}
	term := terminalApp(cfg)
	if err := OpenAgentTerminal(s.NewStateBase, term, repo.Path, *agent, repos.Prompt(repo.Name, repo.Path, repo.DefaultBranch)); err != nil {
		writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error()})
		return
	}
	writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: fmt.Sprintf("opened %s in %s for %s", agent.Name, term, repo.Name)})
}

// OpenAgentTerminal writes a launch script (cd, run the agent with the
// prompt, then stay in a login shell) and opens it in the terminal. A var
// so tests do not open windows.
var OpenAgentTerminal = func(stateBase, term, dir string, a agentDef, prompt string) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return errors.New("opening a terminal is macOS and Windows only for now; copy the prompt instead")
	}
	launchDir := filepath.Join(stateBase, "agent-launch")
	if err := os.MkdirAll(launchDir, 0o700); err != nil {
		return err
	}
	// Old launch files are only needed for the second the terminal takes
	// to read them.
	if old, _ := filepath.Glob(filepath.Join(launchDir, "learn-*")); len(old) > 0 {
		for _, f := range old {
			if fi, err := os.Stat(f); err == nil && time.Since(fi.ModTime()) > time.Hour {
				os.Remove(f)
			}
		}
	}
	id := fmt.Sprintf("learn-%d", time.Now().UnixNano())
	if runtime.GOOS == "windows" {
		return openWindowsTerminal(launchDir, id, dir, a, prompt)
	}
	cmd := a.Bin
	if a.Flag != "" {
		cmd += " " + a.Flag
	}
	script := fmt.Sprintf("#!/bin/zsh -l\ncd %s || exit 1\n%s %s\nexec /bin/zsh -l\n", shq(dir), cmd, shq(prompt))
	scriptPath := filepath.Join(launchDir, id+".command")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return err
	}
	switch term {
	case "warp":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		cfgDir := filepath.Join(home, ".warp", "launch_configurations")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			return err
		}
		// One file, overwritten each time: Warp lists every file here in
		// its launch menu.
		yaml := fmt.Sprintf("---\nname: stackfork-learn\nwindows:\n  - tabs:\n      - title: %q\n        layout:\n          cwd: %q\n          commands:\n            - exec: %q\n",
			"learn "+filepath.Base(dir)+" ("+a.Name+")", dir, scriptPath)
		if err := os.WriteFile(filepath.Join(cfgDir, "stackfork-learn.yaml"), []byte(yaml), 0o644); err != nil {
			return err
		}
		return exec.Command("open", "warp://launch/stackfork-learn.yaml").Run()
	case "iterm":
		return exec.Command("open", "-a", "iTerm", scriptPath).Run()
	case "terminal":
		return exec.Command("open", "-a", "Terminal", scriptPath).Run()
	}
	return fmt.Errorf("unknown terminal %q in the config; use warp, iterm or terminal", term)
}

// openWindowsTerminal writes the prompt and a .ps1 launcher next to each
// other (same 0o700 dir) and opens a PowerShell window running it: Windows
// Terminal when wt.exe is on PATH, else a plain PowerShell in its own console.
func openWindowsTerminal(launchDir, id, dir string, a agentDef, prompt string) error {
	promptPath := filepath.Join(launchDir, id+".prompt.txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o600); err != nil {
		return err
	}
	ps1 := filepath.Join(launchDir, id+".ps1")
	if err := os.WriteFile(ps1, psLauncherFile(psLauncherScript(dir, a.Bin, a.Flag, promptPath)), 0o600); err != nil {
		return err
	}
	if wt, err := exec.LookPath("wt.exe"); err == nil && wtUsable(dir) && wtUsable(ps1) {
		return exec.Command(wt, "-d", dir, "powershell", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", ps1).Start()
	}
	c := exec.Command("powershell", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", ps1)
	c.SysProcAttr = newConsoleAttr()
	return c.Start()
}
