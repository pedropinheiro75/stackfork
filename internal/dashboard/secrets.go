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
	"github.com/pedropinheiro75/stackfork/internal/config"
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
var SecretDialogAvailable = func() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "windows" }

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
	exists map[string]knownRef // keychain existence checks, kept for existsTTL: a page poll must not run a tool every 3 s
	dialog sync.Mutex          // one value dialog at a time
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
	delete(s.exists, ref)
}

const existsTTL = 30 * time.Second

func (s *secretsState) rememberExists(ref string, st lifecycle.RefStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exists == nil {
		s.exists = map[string]knownRef{}
	}
	s.exists[ref] = knownRef{st, time.Now()}
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
	for ref, k := range s.exists {
		if _, checked := out[ref]; checked {
			continue
		}
		if time.Since(k.at) > existsTTL {
			delete(s.exists, ref)
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
	known := s.secrets.snapshot()
	rows, err := lifecycle.SecretRows(repo, false, known, nil, s.NewStateBase, s.LegacyStateBase)
	if err != nil {
		return nil, err
	}
	out := make([]secretJSON, 0, len(rows))
	for _, r := range rows {
		if _, had := known[r.Reference]; !had && strings.HasPrefix(r.Reference, "keychain:") {
			s.secrets.rememberExists(r.Reference, lifecycle.RefStatus{Status: secretstore.Status(r.Status), Detail: r.Detail})
		}
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

// secretView is a row of the Repos panel's Secrets table.
type secretView struct {
	secretJSON
	Chip   chipView
	Item   string // the keychain item, "" for any other reference
	Repo   string
	SetCmd string // copy-only: printenv VAR | stackfork secret set ITEM
	MapCmd string // copy-only: stackfork secret map REPO NAME=REF
	Unmap  string // copy-only, only for a name the machine map holds
}

// secretChip is the StatusChip of a row: glyph and word, never colour alone.
func secretChip(r secretJSON) chipView {
	detail := r.Detail
	switch {
	case r.Status == StatusShellOnly:
		return chipView{Class: "off", Glyph: glyphOff, Label: "Shell only", Title: shellOnlyNote}
	case r.Reference == "":
		return chipView{Class: "failed", Glyph: glyphFailed, Label: "Missing", Title: "no reference on this machine; map it with stackfork secret map"}
	}
	switch secretstore.Status(r.Status) {
	case secretstore.Resolves:
		return chipView{Class: "ok", Glyph: glyphOK, Label: "Resolves"}
	case secretstore.Missing:
		return chipView{Class: "failed", Glyph: glyphFailed, Label: "Missing", Title: detail}
	case secretstore.Locked:
		return chipView{Class: "attention", Glyph: glyphAttention, Label: "Locked", Title: detail}
	case secretstore.NotChecked:
		return chipView{Class: "off", Glyph: glyphOff, Label: "Not checked", Title: "Check reads it; 1Password may ask you to unlock it"}
	case secretstore.Unavailable:
		return chipView{Class: "attention", Glyph: glyphAttention, Label: "Unavailable", Title: detail}
	}
	return chipView{Class: "failed", Glyph: glyphFailed, Label: "Error", Title: detail}
}

// repoSecrets are the rows of the Secrets table, or nil when the repository
// has no declared or mapped name (the section is then not shown). It starts
// no store prompt.
func (s *Server) repoSecrets(repo repos.Repo, cfg *config.Config) []secretView {
	if !hasSecretNames(repo.Path, cfg) {
		return nil
	}
	rows, err := s.secretRows(repo.Path)
	if err != nil {
		return nil
	}
	out := make([]secretView, 0, len(rows))
	for _, r := range rows {
		v := secretView{secretJSON: r, Chip: secretChip(r), Repo: repo.Path}
		v.MapCmd = "stackfork secret map " + shq(repo.Path) + " " + r.Name + "=REF"
		if strings.HasPrefix(r.Reference, "keychain:") {
			v.Item = strings.TrimPrefix(r.Reference, "keychain:")
			v.SetCmd = "printenv VAR | stackfork secret set " + v.Item
		}
		if r.Source == lifecycle.SourceMap {
			v.Unmap = "stackfork secret unmap " + shq(repo.Path) + " " + r.Name
		}
		out = append(out, v)
	}
	return out
}

// hasSecretNames reports whether the repository has a declared or mapped
// secret name.
func hasSecretNames(repo string, cfg *config.Config) bool {
	return cfg != nil && (len(cfg.SecretsFor(repo)) > 0 || len(lifecycle.DeclaredSecrets(repo, repo)) > 0)
}
