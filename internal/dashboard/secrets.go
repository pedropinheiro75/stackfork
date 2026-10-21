package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
)

// StatusShellOnly is what a row shows for an env: reference: the dashboard
// does not see the shell the person starts stackfork from (design D4).
const StatusShellOnly = "shell only"

const shellOnlyNote = "the dashboard does not see your shell environment"

// SecretDialogAvailable reports whether this system has a native
// hidden-answer dialog; without one a row shows the command instead. A var
// so tests can answer it.
var SecretDialogAvailable = func() bool { return runtime.GOOS == "darwin" }

// AskSecretValue opens the dialog that asks a person for a value and returns
// what they typed. It names the repository, the secret and the item so a
// forged request cannot pass for anything else. A var so tests can answer it.
var AskSecretValue func(repo, name, item string) (string, error)

// secretsState holds the statuses a Check found, by reference, in memory only
// and for as long as the value cache (design D11, D14). A page load reads
// them; it never starts a store prompt.
type secretsState struct {
	mu     sync.Mutex
	known  map[string]knownRef
	dialog sync.Mutex // one value dialog at a time
}

type knownRef struct {
	lifecycle.RefStatus
	at time.Time
}

func (s *secretsState) remember(ref string, st lifecycle.RefStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.known == nil {
		s.known = map[string]knownRef{}
	}
	s.known[ref] = knownRef{st, time.Now()}
}

func (s *secretsState) forget(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.known, ref)
}

func (s *secretsState) snapshot() map[string]lifecycle.RefStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]lifecycle.RefStatus{}
	for ref, k := range s.known {
		if time.Since(k.at) > lifecycle.SecretCacheTTL {
			delete(s.known, ref)
			continue
		}
		out[ref] = k.RefStatus
	}
	return out
}

// secretJSON is one row of /api/secrets: names, references and statuses,
// never a value.
type secretJSON struct {
	lifecycle.SecretRow
	CanSet bool `json:"can_set"`
}

type secretsResponse struct {
	Repo    string       `json:"repo"`
	Secrets []secretJSON `json:"secrets"`
}

// secretRows are the rows of a repository from what is known without a
// prompt. An env: reference is never evaluated: the dashboard's own
// environment is not the person's shell.
func (s *Server) secretRows(repo string) ([]secretJSON, error) {
	rows, err := lifecycle.SecretRows(repo, false, s.secrets.snapshot(), s.NewStateBase, s.LegacyStateBase)
	if err != nil {
		return nil, err
	}
	out := make([]secretJSON, 0, len(rows))
	for _, r := range rows {
		if strings.HasPrefix(r.Reference, "env:") {
			r.Status, r.Detail = StatusShellOnly, shellOnlyNote
		}
		out = append(out, secretJSON{SecretRow: r, CanSet: strings.HasPrefix(r.Reference, "keychain:") && SecretDialogAvailable()})
	}
	return out, nil
}

func writeSecrets(w http.ResponseWriter, repo string, rows []secretJSON) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(secretsResponse{Repo: repo, Secrets: rows}) //nolint:errcheck // client gone
}

func (s *Server) secretsAuthorized(w http.ResponseWriter, r *http.Request) bool {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return false
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) secretsRepo(w http.ResponseWriter, path string) *repos.Repo {
	repo := s.scannedRepo(path)
	if path == "" || repo == nil {
		writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a repo found by the scan under a configured folder"})
		return nil
	}
	return repo
}

// handleSecrets is GET /api/secrets?repo=PATH: the rows, from what is
// already known. It starts no store prompt.
func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) {
	repo := s.secretsRepo(w, r.URL.Query().Get("repo"))
	if repo == nil {
		return
	}
	rows, err := s.secretRows(repo.Path)
	if err != nil {
		writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
		return
	}
	writeSecrets(w, repo.Path, rows)
}

type secretsRequest struct {
	Repo string `json:"repo"`
	Name string `json:"name,omitempty"`
}

func decodeSecretsRequest(w http.ResponseWriter, r *http.Request) (secretsRequest, bool) {
	var req secretsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

// handleSecretsCheck is POST /api/secrets/check {repo}: the one place a page
// load's worth of references is read in full (a 1Password prompt is
// possible), on a person's click. It checks every instance of the repository,
// which also remembers their values for the log view's redaction (D11).
func (s *Server) handleSecretsCheck(w http.ResponseWriter, r *http.Request) {
	if !s.secretsAuthorized(w, r) {
		return
	}
	req, ok := decodeSecretsRequest(w, r)
	if !ok {
		return
	}
	repo := s.secretsRepo(w, req.Repo)
	if repo == nil {
		return
	}
	key := "secrets|" + repo.Path
	if !s.tryLockProject(key) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "a check is already running for this repo"})
		return
	}
	defer s.unlockProject(key)

	seen := map[string]bool{}
	for _, wt := range repo.Worktrees {
		if wt.Instance == nil {
			continue
		}
		dir, _, _, info, err := s.resolveInstance(wt.Instance.Project)
		if err != nil {
			continue
		}
		if _, statuses, err := lifecycle.CheckSecretsStatus(dir, info); err == nil {
			for ref, st := range statuses {
				s.secrets.remember(ref, st)
				seen[ref] = true
			}
		}
	}
	rows, err := s.secretRows(repo.Path)
	if err != nil {
		writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
		return
	}
	for _, row := range rows { // references no instance carries
		ref := row.Reference
		if ref == "" || seen[ref] || strings.HasPrefix(ref, "env:") {
			continue
		}
		seen[ref] = true
		_, st, serr := compose.CheckSecret(ref)
		rs := lifecycle.RefStatus{Status: st}
		if serr != nil && st != secretstore.Resolves {
			rs.Detail, _, _ = strings.Cut(serr.Error(), "\n")
		}
		s.secrets.remember(ref, rs)
	}
	if rows, err = s.secretRows(repo.Path); err != nil {
		writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
		return
	}
	writeSecrets(w, repo.Path, rows)
}

// handleSecretsSet is POST /api/secrets/set {repo, name}: a native dialog
// asks the person for the value and the server writes it to the store. The
// value never travels over HTTP, in either direction (D3, D14).
func (s *Server) handleSecretsSet(w http.ResponseWriter, r *http.Request) {
	if !s.secretsAuthorized(w, r) {
		return
	}
	req, ok := decodeSecretsRequest(w, r)
	if !ok {
		return
	}
	repo := s.secretsRepo(w, req.Repo)
	if repo == nil {
		return
	}
	rows, err := s.secretRows(repo.Path)
	if err != nil {
		writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
		return
	}
	var ref string
	found := false
	for _, row := range rows {
		if row.Name == req.Name {
			ref, found = row.Reference, true
		}
	}
	if !found {
		writeActionResult(w, http.StatusNotFound, actionResponse{Message: "not a declared or mapped secret of this repo"})
		return
	}
	kind, item, perr := secretstore.ParseRef(ref)
	if perr != nil || kind != secretstore.KindKeychain {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: "only a keychain: reference takes a value here; map the name to one with stackfork secret map"})
		return
	}
	if !SecretDialogAvailable() {
		writeActionResult(w, http.StatusNotImplemented, actionResponse{Message: "this system has no value dialog; run printenv VAR | stackfork secret set " + item})
		return
	}
	if !s.secrets.dialog.TryLock() {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "a value dialog is already open"})
		return
	}
	defer s.secrets.dialog.Unlock()

	value, err := AskSecretValue(repo.Name, req.Name, item)
	if err != nil {
		writeActionResult(w, http.StatusOK, actionResponse{Message: "nothing stored: " + scrub(err.Error(), value)})
		return
	}
	if err := secretstore.Store(item, value); err != nil {
		writeActionResult(w, http.StatusOK, actionResponse{Message: "nothing stored: " + scrub(err.Error(), value)})
		return
	}
	s.secrets.forget(ref)
	writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "stored keychain:" + item})
}

// scrub removes a value from a message that should never have held it.
func scrub(msg, value string) string {
	if value != "" {
		msg = strings.ReplaceAll(msg, value, "…")
	}
	line, _, _ := strings.Cut(msg, "\n")
	return line
}
