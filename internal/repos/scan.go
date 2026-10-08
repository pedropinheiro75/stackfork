package repos

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// RootInfo is one configured root and how many repos it holds.
type RootInfo struct {
	Path  string `json:"path"`
	Repos int    `json:"repos"`
}

// Result is what `stackfork repos --json` and GET /api/repos return.
type Result struct {
	Configured bool       `json:"configured"`
	Roots      []RootInfo `json:"roots"`
	Exclude    []string   `json:"exclude"`
	MaxDepth   int        `json:"max_depth"`
	Repos      []Repo     `json:"repos"`
}

// Options drives one scan.
type Options struct {
	Config      *config.Config
	Instances   []inventory.Instance
	ProfileDirs []string
	CacheFile   string // "" disables the cache
	Rescan      bool   // ignore the cache, re-read every repo
}

const workers = 8

// Scan walks the configured roots and builds the Result. With no roots it
// scans nothing.
func Scan(o Options) Result {
	res := Result{Roots: []RootInfo{}, Exclude: []string{}, Repos: []Repo{}, MaxDepth: o.Config.Depth()}
	res.Exclude = append(res.Exclude, o.Config.Exclude...)
	roots := o.Config.ResolvedRoots()
	res.Configured = len(o.Config.Roots) > 0
	if len(roots) == 0 {
		return res
	}
	paths := Walk(roots, o.Config.Exclude, o.Config.Depth())

	cache := loadCache(o.CacheFile, o.Rescan)
	repos := make([]*Repo, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d, err := cache.read(p)
			if err != nil {
				return
			}
			repos[i] = build(p, roots, d, o)
		}()
	}
	wg.Wait()
	cache.save(o.CacheFile)

	counts := map[string]int{}
	for _, r := range repos {
		if r == nil {
			continue
		}
		res.Repos = append(res.Repos, *r)
		for _, root := range roots {
			if config.Inside(root, r.Path) {
				counts[root]++
			}
		}
	}
	sort.Slice(res.Repos, func(i, j int) bool {
		a, b := res.Repos[i], res.Repos[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	for _, r := range roots {
		res.Roots = append(res.Roots, RootInfo{Path: r, Repos: counts[r]})
	}
	return res
}

// build joins one repo's git data with readiness and instance state.
func build(path string, roots []string, d gitData, o Options) *Repo {
	r := &Repo{
		Name: filepath.Base(path), Path: path, DefaultBranch: d.DefaultBranch,
		Worktrees: d.Worktrees, Branches: d.Branches, LastCommit: d.LastCommit,
	}
	for _, root := range roots {
		if config.Inside(root, path) {
			g, _ := filepath.Rel(root, filepath.Dir(path))
			if g != "." {
				r.Group = filepath.ToSlash(g)
			}
		}
	}
	checkouts := []string{path}
	for i := range r.Worktrees {
		w := &r.Worktrees[i]
		checkouts = append(checkouts, w.Path)
		w.Instance = instanceFor(o.Instances, w.Path, w.Branch)
		if w.Instance != nil && (w.Instance.State == inventory.StateRunning || w.Instance.State == inventory.StatePartial) {
			r.Running++
		}
	}
	r.Readiness, r.ReadinessReason = ReadinessOf(r.Name, checkouts, o.Instances, o.ProfileDirs)
	return r
}

// instanceFor picks the instance prepared for this checkout and branch.
func instanceFor(insts []inventory.Instance, root, branch string) *inventory.InstanceRef {
	for _, i := range insts {
		if i.Root == root && i.Branch == branch {
			return &inventory.InstanceRef{Project: i.Project, State: i.State}
		}
	}
	return nil
}

// FindRepo returns the scanned repo whose name or path matches arg
// (a name must be unambiguous).
func FindRepo(res Result, arg string) (*Repo, error) {
	var hits []*Repo
	for i := range res.Repos {
		r := &res.Repos[i]
		if r.Path == arg {
			return r, nil
		}
		if r.Name == arg {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return nil, errNotFound(arg)
	case 1:
		return hits[0], nil
	}
	var ps []string
	for _, h := range hits {
		ps = append(ps, h.Path)
	}
	return nil, errAmbiguous(arg, strings.Join(ps, ", "))
}
