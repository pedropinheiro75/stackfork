package lifecycle

import (
	"fmt"
	"sort"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Where an effective reference comes from (design D7, D10).
const (
	SourceFlag     = "flag"
	SourceMap      = "map"
	SourceManifest = "manifest"
	SourceFolder   = "folder"
	SourceSibling  = "sibling"
)

// Layer is the references one source contributes.
type Layer struct {
	Source string
	Refs   map[string]string
}

// Effective merges layers, lowest precedence first, into the effective
// references and the source of each (D7): the command's --secret flags beat
// the machine map, which beats a folder's, which beats the manifest, which
// beats a sibling worktree.
func Effective(layers ...Layer) (refs, source map[string]string) {
	refs, source = map[string]string{}, map[string]string{}
	for _, l := range layers {
		for n, r := range l.Refs {
			refs[n], source[n] = r, l.Source
		}
	}
	return refs, source
}

// MappedSecrets are the references the machine map holds for the repository
// of root (its main checkout).
func MappedSecrets(root string) (map[string]string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	main := identity.MainRepoOf(root)
	if main == "" {
		main = root
	}
	return cfg.SecretsFor(main), nil
}

// FolderSecrets are the references mapped on the folders above the repository
// of root, limited to the names it uses (R3): declared by its knowledge or
// in used (the names its instance recorded). A folder never adds a name. from
// is the folder each reference comes from.
func FolderSecrets(root string, used map[string]string) (refs, from map[string]string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	main := mainOf(root)
	inh, inhFrom := cfg.InheritedSecretsFor(main)
	uses := map[string]bool{}
	for n := range used {
		uses[n] = true
	}
	for _, d := range DeclaredSecrets(root, main) {
		uses[d.Name] = true
	}
	refs, from = map[string]string{}, map[string]string{}
	for n, r := range inh {
		if uses[n] {
			refs[n], from[n] = r, inhFrom[n]
		}
	}
	return refs, from, nil
}

// InstanceSecrets are the references an `up` uses (D7): the manifest's, each
// overridden by a folder's and then the machine map's without a new prepare. A mapped name the
// manifest does not record cannot be rendered into the snapshot; it is
// returned in unprepared, sorted.
func InstanceSecrets(info *state.Manifest, mapped map[string]string) (refs, source map[string]string, unprepared []string) {
	have := info.ResolvedSecrets()
	over := map[string]string{}
	for n, r := range mapped {
		if _, ok := have[n]; ok {
			over[n] = r
		} else {
			unprepared = append(unprepared, n)
		}
	}
	sort.Strings(unprepared)
	// A folder reference only replaces a name the manifest records: it never
	// adds one, so it cannot make an instance "unprepared".
	folder, _, _ := FolderSecrets(info.Root, have)
	fover := map[string]string{}
	for n, r := range folder {
		if _, ok := have[n]; ok {
			fover[n] = r
		}
	}
	refs, source = Effective(Layer{SourceManifest, have}, Layer{SourceFolder, fover}, Layer{SourceMap, over})
	return refs, source, unprepared
}

// UnpreparedError is what an up or run says when the map names a secret the
// instance was prepared without.
func UnpreparedError(name string) error {
	return fmt.Errorf("secret %s is mapped but this instance was prepared without it; run stackfork start-env", name)
}
