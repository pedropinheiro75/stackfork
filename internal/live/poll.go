package live

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListFiles returns the regular files of a watch entry: the file itself, or
// the files of a directory to depth 2 (design D4). A missing path has none.
func ListFiles(p string) []string {
	fi, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if !fi.IsDir() {
		return []string{p}
	}
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		es, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range es {
			switch {
			case e.Type().IsRegular():
				out = append(out, filepath.Join(dir, e.Name()))
			case e.IsDir() && depth < 2:
				walk(filepath.Join(dir, e.Name()), depth+1)
			}
			// symbolic links inside a directory are not followed
		}
	}
	walk(p, 1)
	return out
}

// Change is one path whose digest differs from the baseline. Digest is ""
// for a removed file.
type Change struct {
	Path     string `json:"path"` // relative to the checkout, "/" separated
	Category string `json:"category"`
	Service  string `json:"service,omitempty"`
	Digest   string `json:"-"`
	Kind     string `json:"kind"` // changed, added, removed
}

type fileStat struct {
	size, mtime int64
	digest      string
}

// Poller compares a watch set with a baseline. It stats every file and
// hashes one only when its size or modification time moved, so an editor
// that rewrites a file unchanged is not a change.
type Poller struct {
	root     string
	entries  []Entry
	baseline map[string]string // rel path -> sha256, absent files have no key
	stats    map[string]fileStat
	current  map[string]string
}

// NewPoller starts with an empty stat cache and the given baseline digests
// (relative path -> SHA-256). checkout is the root the entries are relative to.
func NewPoller(checkout string, entries []Entry, baseline map[string]string) *Poller {
	if baseline == nil {
		baseline = map[string]string{}
	}
	return &Poller{root: checkout, entries: entries, baseline: baseline, stats: map[string]fileStat{}, current: map[string]string{}}
}

// Poll reads the watch set once and returns every path that differs from the
// baseline, sorted by path. A read error leaves that file as last seen: no
// change this tick.
func (p *Poller) Poll() []Change {
	now := map[string]string{}
	meta := map[string]Entry{}
	for _, e := range p.entries {
		for _, f := range ListFiles(e.Path) {
			rel, err := filepath.Rel(p.root, f)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			d, ok := p.digest(f)
			if !ok {
				if old, had := p.current[rel]; had {
					now[rel], meta[rel] = old, e
				}
				continue
			}
			now[rel], meta[rel] = d, e
		}
	}
	p.current = now

	var out []Change
	for rel, d := range now {
		switch b, had := p.baseline[rel]; {
		case !had:
			out = append(out, Change{Path: rel, Category: meta[rel].Category, Service: meta[rel].Service, Digest: d, Kind: "added"})
		case b != d:
			out = append(out, Change{Path: rel, Category: meta[rel].Category, Service: meta[rel].Service, Digest: d, Kind: "changed"})
		}
	}
	for rel := range p.baseline {
		if _, here := now[rel]; here {
			continue
		}
		if e, ok := p.owner(rel); ok {
			out = append(out, Change{Path: rel, Category: e.Category, Service: e.Service, Kind: "removed"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// owner is the entry a baseline path belongs to (the file, or under the dir).
func (p *Poller) owner(rel string) (Entry, bool) {
	for _, e := range p.entries {
		if e.Rel == rel || strings.HasPrefix(rel, e.Rel+"/") {
			return e, true
		}
	}
	return Entry{}, false
}

// digest returns the SHA-256 of f, reusing the last one when size and
// modification time did not move. ok is false on a read error.
func (p *Poller) digest(f string) (string, bool) {
	fi, err := os.Stat(f)
	if err != nil {
		return "", false
	}
	st := fileStat{size: fi.Size(), mtime: fi.ModTime().UnixNano()}
	if old, ok := p.stats[f]; ok && old.size == st.size && old.mtime == st.mtime {
		return old.digest, true
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	st.digest = hex.EncodeToString(sum[:])
	p.stats[f] = st
	return st.digest, true
}

// Digests is what the last Poll saw, for writing a baseline.
func (p *Poller) Digests() map[string]string {
	out := make(map[string]string, len(p.current))
	for k, v := range p.current {
		out[k] = v
	}
	return out
}

// SetBaseline replaces the baseline digests.
func (p *Poller) SetBaseline(b map[string]string) {
	if b == nil {
		b = map[string]string{}
	}
	p.baseline = b
}
