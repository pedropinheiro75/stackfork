package knowledge

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Live-watch actions (live-mode D5).
const (
	LiveRebuild = "rebuild"
	LivePrepare = "prepare"
	LiveMigrate = "migrate"
)

// LiveWatch is one `live.watch` entry. Its fields match live.Override, so
// the loop converts with live.Override(w).
type LiveWatch struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Service string `json:"service,omitempty"`
}

// Live is the optional `live` object of knowledge.json. It is not part of
// the trust hash (D5): every action is one live mode already runs.
type Live struct {
	Watch  []LiveWatch `json:"watch,omitempty"`
	Ignore []string    `json:"ignore,omitempty"`
}

// IsZero lets `omitzero` drop an empty object.
func (l Live) IsZero() bool { return len(l.Watch) == 0 && len(l.Ignore) == 0 }

// ValidateLivePath accepts a clean relative path inside the checkout, with
// forward slashes. The error never echoes the path.
func ValidateLivePath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("path is empty")
	case strings.IndexFunc(p, unicode.IsControl) >= 0:
		return fmt.Errorf("path has a control character")
	case strings.HasPrefix(p, "-"):
		return fmt.Errorf("path starts with '-'")
	case strings.Contains(p, "\\") || path.IsAbs(p) || !filepath.IsLocal(p) || path.Clean(p) != p:
		return fmt.Errorf("path must be relative to the checkout, with forward slashes, no .. or ./")
	}
	return nil
}

// ValidateLive checks actions, services and paths.
func ValidateLive(l Live) error {
	for i, w := range l.Watch {
		if err := ValidateLivePath(w.Path); err != nil {
			return fmt.Errorf("live.watch[%d]: %w", i, err)
		}
		switch w.Action {
		case LiveRebuild:
			if w.Service == "" {
				return fmt.Errorf("live.watch[%d]: rebuild needs a service", i)
			}
		case LivePrepare, LiveMigrate:
			if w.Service != "" {
				return fmt.Errorf("live.watch[%d]: service is only for rebuild", i)
			}
		default:
			return fmt.Errorf("live.watch[%d]: unknown action (rebuild, prepare or migrate)", i)
		}
		if strings.IndexFunc(w.Service, unicode.IsControl) >= 0 {
			return fmt.Errorf("live.watch[%d]: service has a control character", i)
		}
	}
	for i, p := range l.Ignore {
		if err := ValidateLivePath(p); err != nil {
			return fmt.Errorf("live.ignore[%d]: %w", i, err)
		}
	}
	return nil
}

// parseLive decodes the "live" key strictly.
func parseLive(raw json.RawMessage) (Live, error) {
	var l Live
	if len(raw) == 0 || string(raw) == "null" {
		return l, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return l, fmt.Errorf("live must be an object with watch and ignore")
	}
	for _, k := range sortedRawKeys(obj) {
		if k != "watch" && k != "ignore" {
			return l, fmt.Errorf("live: key %q is not allowed", k)
		}
	}
	if v, ok := obj["ignore"]; ok {
		if err := json.Unmarshal(v, &l.Ignore); err != nil {
			return l, fmt.Errorf("live.ignore must be a list of paths")
		}
	}
	if v, ok := obj["watch"]; ok {
		var entries []json.RawMessage
		if err := json.Unmarshal(v, &entries); err != nil {
			return l, fmt.Errorf("live.watch must be a list of objects")
		}
		for i, e := range entries {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(e, &m); err != nil || m == nil {
				return l, fmt.Errorf("live.watch[%d] must be an object", i)
			}
			for _, k := range sortedRawKeys(m) {
				if k != "path" && k != "action" && k != "service" {
					return l, fmt.Errorf("live.watch[%d]: key %q is not allowed", i, k)
				}
			}
			var w LiveWatch
			for k, dst := range map[string]*string{"path": &w.Path, "action": &w.Action, "service": &w.Service} {
				if v, ok := m[k]; ok {
					if err := json.Unmarshal(v, dst); err != nil {
						return l, fmt.Errorf("live.watch[%d]: %s must be a string", i, k)
					}
				}
			}
			l.Watch = append(l.Watch, w)
		}
	}
	if err := ValidateLive(l); err != nil {
		return Live{}, err
	}
	return l, nil
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// UpsertLiveWatch adds a watch entry, replacing the one for the same path.
func (k *Knowledge) UpsertLiveWatch(w LiveWatch) error {
	next := k.Live
	next.Watch = append([]LiveWatch(nil), k.Live.Watch...)
	replaced := false
	for i := range next.Watch {
		if next.Watch[i].Path == w.Path {
			next.Watch[i], replaced = w, true
		}
	}
	if !replaced {
		next.Watch = append(next.Watch, w)
	}
	if err := ValidateLive(next); err != nil {
		return err
	}
	k.Live = next
	return nil
}

// RemoveLiveWatch drops the entry for path; it reports whether one was there.
func (k *Knowledge) RemoveLiveWatch(p string) bool {
	for i := range k.Live.Watch {
		if k.Live.Watch[i].Path == p {
			k.Live.Watch = append(k.Live.Watch[:i:i], k.Live.Watch[i+1:]...)
			return true
		}
	}
	return false
}

// AddLiveIgnore adds a path to live.ignore (no duplicates).
func (k *Knowledge) AddLiveIgnore(p string) error {
	if err := ValidateLivePath(p); err != nil {
		return fmt.Errorf("live.ignore: %w", err)
	}
	for _, e := range k.Live.Ignore {
		if e == p {
			return nil
		}
	}
	k.Live.Ignore = append(k.Live.Ignore, p)
	return nil
}

// RemoveLiveIgnore drops a path from live.ignore.
func (k *Knowledge) RemoveLiveIgnore(p string) bool {
	for i, e := range k.Live.Ignore {
		if e == p {
			k.Live.Ignore = append(k.Live.Ignore[:i:i], k.Live.Ignore[i+1:]...)
			return true
		}
	}
	return false
}
