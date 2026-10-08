package dashboard

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// seedAction answers the instance card's "Seed now" / "Never for this
// project". The command comes from the recorded decision, or is detected
// from the instance's own checkout; nothing the request says is executed.
// An already-seeded instance is refused: --force stays a copy-only command.
func (s *Server) seedAction(req actionRequest) actionResponse {
	instanceDir, root, branch, info, err := s.resolveInstance(req.Project)
	if err != nil {
		return actionResponse{Message: err.Error()}
	}
	if err := os.MkdirAll(instanceDir, 0o700); err != nil {
		return actionResponse{Message: err.Error()}
	}
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return actionResponse{Message: err.Error()}
	}
	defer lock.Release()

	decided, cmd, svc := learn.SeedDecision([]string{root}, info)
	if req.Action == "seed-never" {
		lifecycle.SetSeedDecision(instanceDir, learn.SeedNone)
		_ = learn.RecordSeedDecision(root, learn.SeedNone)
		return actionResponse{OK: true, Message: "recorded: never seed " + req.Project}
	}
	if info.Seeded != nil && *info.Seeded {
		return actionResponse{Message: "already seeded; to run it again copy `stackfork --instance " + req.Project + " seed --force`"}
	}
	if decided && !learn.SeedActive(cmd) {
		return actionResponse{Message: "this project is recorded as never seeded"}
	}
	if !decided {
		_, cmd = learn.DetectSeed(root)
		if cmd == "" {
			return actionResponse{Message: "no seed script found in this checkout"}
		}
		lifecycle.SetSeedDecision(instanceDir, cmd)
		_ = learn.RecordSeedDecision(root, cmd)
	}
	f := false
	lifecycle.SetSeeded(instanceDir, &f)
	var out bytes.Buffer
	if !lifecycle.RunSeed(&out, &out, root, branch, req.Project, instanceDir, s.NewStateBase, s.LegacyStateBase, cmd, svc) {
		return actionResponse{Message: lastOutput(out.String(), "seed failed")}
	}
	return actionResponse{OK: true, Message: "seeded " + req.Project}
}

func lastOutput(out, fallback string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return fallback
	}
	return out[strings.LastIndexByte(out, '\n')+1:]
}

// recipeView is a repo's startup-recipe summary for the Repos tab.
type recipeView struct {
	Migrate  string
	Detected string // the seed script found in the repo, if any
	Seed     string // the decided command ("" when none)
	State    string // "seed", "never" or "undecided"
}

// repoRecipe summarises the migrate command and the seed decision for one
// repo: the decision lives in the knowledge recipe if the repo has one,
// else in its instances' manifests.
func (s *Server) repoRecipe(repo repos.Repo) recipeView {
	migrate, detected := learn.DetectSeed(repo.Path)
	v := recipeView{Migrate: migrate, Detected: detected, State: "undecided"}
	decided, cmd, _ := learn.SeedDecision([]string{repo.Path}, nil)
	for _, wt := range repo.Worktrees {
		if decided || wt.Instance == nil {
			continue
		}
		if _, _, _, info, err := s.resolveInstance(wt.Instance.Project); err == nil {
			decided, cmd, _ = learn.SeedDecision(nil, info)
		}
	}
	switch {
	case decided && learn.SeedActive(cmd):
		v.State, v.Seed = "seed", cmd
	case decided:
		v.State = "never"
	}
	return v
}

// handleRecipeSeed sets a repo's seed decision from the Repos tab. It only
// records; it never runs anything.
func (s *Server) handleRecipeSeed(w http.ResponseWriter, req actionRequest) {
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
	var value string
	switch req.Choice {
	case "detected":
		if _, value = learn.DetectSeed(repo.Path); value == "" {
			writeActionResult(w, http.StatusOK, actionResponse{Message: "no seed script found in this repo"})
			return
		}
	case "never":
		value = learn.SeedNone
	case "clear":
	default:
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: "unknown choice"})
		return
	}
	if !s.tryLockProject(repo.Path) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "an action is already running for this repo"})
		return
	}
	defer s.unlockProject(repo.Path)
	defer s.rescanAsync(false)
	defer s.cache.invalidate()

	if _, err := os.Stat(filepath.Join(repo.Path, ".stackfork", "knowledge.json")); err == nil {
		if err := learn.RecordSeedDecision(repo.Path, value); err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error()})
			return
		}
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "recorded in " + repo.Name + "'s knowledge recipe"})
		return
	}
	n := 0
	for _, wt := range repo.Worktrees {
		if wt.Instance == nil {
			continue
		}
		if dir, _, _, _, err := s.resolveInstance(wt.Instance.Project); err == nil {
			lifecycle.SetSeedDecision(dir, value)
			n++
		}
	}
	if n == 0 {
		writeActionResult(w, http.StatusOK, actionResponse{Message: fmt.Sprintf("%s has no knowledge file and no instance yet to record this in; start it once or run `stackfork learn`", repo.Name)})
		return
	}
	writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: fmt.Sprintf("recorded in %d instance(s) of %s", n, repo.Name)})
}
