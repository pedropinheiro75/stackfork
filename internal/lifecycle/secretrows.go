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
	From      string `json:"from,omitempty"` // the folder, for source folder
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
// map's references (and the flags' of this command), then the folders'
// (only for names it uses), the recorded
// references of the repository's own instance, then those of its newest
// prepared instance in another worktree (source sibling), then declared names
// with no reference at all. check reads every
// value (and drops it); otherwise nothing that would prompt is read, and a
// reference in known keeps the status an earlier check gave it. root is the
// checkout asked about (its own knowledge declares names, as LoadFor finds
// it); repo is its main checkout, which keys the machine map.
func SecretRows(root, repo string, check bool, known map[string]RefStatus, flags map[string]string, newBase, legacyBase string) ([]SecretRow, error) {
	return SecretRowsAt(repo, root, repo, check, known, flags, newBase, legacyBase)
}

// SecretRowsAt is SecretRows with the checkout whose instance counts as the
// manifest row (own): the repository's main checkout for SecretRows, the
// checkout a command runs in for `secret ls` without REPO. Every other
// checkout's recorded references are the sibling row.
func SecretRowsAt(own, root, repo string, check bool, known map[string]RefStatus, flags map[string]string, newBase, legacyBase string) ([]SecretRow, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	ownRefs, sibling := manifestRefsFor(own, repo, newBase, legacyBase)
	files, envFile := RecipeFiles(repo, newBase, legacyBase)
	used := ComposeSecrets(repo, files, envFile) // names the Compose source needs count as declared
	folder, folderFrom, err := FolderSecrets(repo, UsedPlus(ownRefs, repo, files, envFile))
	if err != nil {
		return nil, err
	}
	refs, source := Effective(
		Layer{Source: SourceSibling, Refs: sibling},
		Layer{Source: SourceManifest, Refs: ownRefs},
		Layer{Source: SourceFolder, Refs: folder},
		Layer{Source: SourceMap, Refs: cfg.SecretsFor(repo)},
		Layer{Source: SourceFlag, Refs: flags})
	hints := map[string]string{}
	for _, d := range DeclaredSecrets(root, repo) {
		hints[d.Name] = d.Hint
		if _, ok := refs[d.Name]; !ok {
			refs[d.Name], source[d.Name] = "", "declared"
		}
	}
	for n, f := range used {
		if _, ok := refs[n]; !ok {
			refs[n], source[n] = "", "declared"
			if hints[n] == "" {
				hints[n] = "used by " + f
			}
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
		if row.Source == SourceFolder {
			row.From = folderFrom[n]
		}
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
// instance of the checkout self (own) and by the newest one of any other
// checkout of the repository (sibling), or nil.
func manifestRefsFor(self, repo, newBase, legacyBase string) (own, sibling map[string]string) {
	want, _ := filepath.EvalSymlinks(repo)
	selfWant, _ := filepath.EvalSymlinks(self)
	var ownTime, sibTime int64
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
			root, _ := filepath.EvalSymlinks(m.Root)
			t := fi.ModTime().UnixNano()
			switch {
			case root == selfWant && t > ownTime:
				own, ownTime = m.ResolvedSecrets(), t
			case root != selfWant && t > sibTime:
				sibling, sibTime = m.ResolvedSecrets(), t
			}
		}
	}
	return own, sibling
}

func mainOf(path string) string {
	if m := identity.MainRepoOf(path); m != "" {
		return m
	}
	return path
}
