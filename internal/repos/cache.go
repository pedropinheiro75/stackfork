package repos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

const cacheVersion = 2 // 2: worktrees carry ahead/behind/upstream/last_commit

type cacheEntry struct {
	Sig  string  `json:"sig"`
	Data gitData `json:"data"`
}

type cacheFile struct {
	Version int                   `json:"version"`
	Repos   map[string]cacheEntry `json:"repos"`
}

// cache reuses a repo's git data while its .git metadata is unchanged
// (design.md D2): no git process runs for an unchanged repo.
type cache struct {
	mu    sync.Mutex
	f     cacheFile
	dirty bool
	fresh bool // Rescan: never reuse
}

func loadCache(path string, rescan bool) *cache {
	c := &cache{f: cacheFile{Version: cacheVersion, Repos: map[string]cacheEntry{}}, fresh: rescan}
	if path == "" || rescan {
		c.dirty = rescan
		return c
	}
	var f cacheFile
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &f) == nil && f.Version == cacheVersion && f.Repos != nil {
		c.f = f
	}
	return c
}

func (c *cache) read(repo string) (gitData, error) {
	sig := signature(repo)
	c.mu.Lock()
	e, ok := c.f.Repos[repo]
	c.mu.Unlock()
	if ok && sig != "" && e.Sig == sig && !c.fresh {
		return e.Data, nil
	}
	d, err := readGit(repo)
	if err != nil {
		return d, err
	}
	c.mu.Lock()
	c.f.Repos[repo] = cacheEntry{Sig: sig, Data: d}
	c.dirty = true
	c.mu.Unlock()
	return d, nil
}

func (c *cache) save(path string) {
	if path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		state.WritePrivate(path, c.f) //nolint:errcheck // a cache that cannot be written is just a slower next scan
	}
}

// signature fingerprints the git metadata that the cached data derives
// from: HEAD, index, config, packed refs, every ref, and each linked
// worktree's HEAD/index/gitdir (plus whether its folder still exists).
// ponytail: edits to tracked files that never touch the index do not change
// it, so a dirty flag can lag until the next metadata change or a rescan.
func signature(repo string) string {
	gitDir := filepath.Join(repo, ".git")
	var parts []string
	stat := func(p string) {
		if fi, err := os.Stat(p); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", strings.TrimPrefix(p, gitDir), fi.ModTime().UnixNano(), fi.Size()))
		}
	}
	for _, n := range []string{"", "HEAD", "index", "config", "packed-refs"} {
		stat(filepath.Join(gitDir, n))
	}
	filepath.WalkDir(filepath.Join(gitDir, "refs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			stat(p)
		}
		return nil
	})
	wts, _ := filepath.Glob(filepath.Join(gitDir, "worktrees", "*"))
	for _, w := range wts {
		for _, n := range []string{"", "HEAD", "index", "gitdir"} {
			stat(filepath.Join(w, n))
		}
		if b, err := os.ReadFile(filepath.Join(w, "gitdir")); err == nil {
			g := strings.TrimSpace(string(b))
			_, err := os.Stat(g)
			parts = append(parts, fmt.Sprintf("%s:%v", g, err == nil))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}
