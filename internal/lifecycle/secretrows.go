package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SecretRow is one secret name of a repository: its reference, where the
// reference comes from and its status. Never a value.
type SecretRow struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	Source    string `json:"source"`
	Hint      string `json:"hint,omitempty"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}

// RefStatus is the outcome of an earlier check of one reference.
type RefStatus struct {
	Status secretstore.Status
	Detail string
}

// SecretRows builds one row per secret name of a repository: the machine
// map's references, then the recorded references of its newest prepared
// instance, then declared names with no reference at all. check reads every
// value (and drops it); otherwise nothing that would prompt is read, and a
// reference in known keeps the status an earlier check gave it.
func SecretRows(repo string, check bool, known map[string]RefStatus, newBase, legacyBase string) ([]SecretRow, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	refs, source := Effective(
		Layer{Source: SourceManifest, Refs: manifestRefsFor(repo, newBase, legacyBase)},
		Layer{Source: SourceMap, Refs: cfg.SecretsFor(repo)})
	hints := map[string]string{}
	for _, d := range DeclaredSecrets(repo, repo) {
		hints[d.Name] = d.Hint
		if _, ok := refs[d.Name]; !ok {
			refs[d.Name], source[d.Name] = "", "declared"
		}
	}
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)
	rows := make([]SecretRow, 0, len(names))
	for _, n := range names {
		row := SecretRow{Name: n, Reference: refs[n], Source: source[n], Hint: hints[n]}
		var st secretstore.Status
		var serr error
		switch k, isKnown := known[row.Reference]; {
		case row.Reference == "":
			st, serr = secretstore.Missing, errors.New("no reference on this machine")
		case isKnown && !check:
			st, serr = k.Status, errors.New(k.Detail)
		case check:
			_, st, serr = compose.CheckSecret(row.Reference)
		default:
			st, serr = compose.ExistsSecret(row.Reference)
		}
		row.Status = string(st)
		if serr != nil && st != secretstore.Resolves {
			row.Detail = firstLineOf(serr.Error())
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// manifestRefsFor are the references recorded by the newest prepared
// instance of the repository (any of its checkouts), or nil.
func manifestRefsFor(repo, newBase, legacyBase string) map[string]string {
	want, _ := filepath.EvalSymlinks(repo)
	var best map[string]string
	var bestTime int64
	for _, base := range []string{newBase, legacyBase} {
		files, _ := filepath.Glob(filepath.Join(base, "*", "manifest.json"))
		for _, f := range files {
			m, err := state.ReadManifest(f)
			if err != nil || m == nil {
				continue
			}
			got, _ := filepath.EvalSymlinks(mainOf(m.Root))
			fi, serr := os.Stat(f)
			if got != want || serr != nil || len(m.ResolvedSecrets()) == 0 {
				continue
			}
			if t := fi.ModTime().UnixNano(); t > bestTime {
				best, bestTime = m.ResolvedSecrets(), t
			}
		}
	}
	return best
}

func mainOf(path string) string {
	if m := identity.MainRepoOf(path); m != "" {
		return m
	}
	return path
}
