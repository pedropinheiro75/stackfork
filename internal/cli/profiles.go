package cli

// Profile discovery for `where` (task 5.2, design.md D3). The CLI only
// lists what a profile directory contains (a context.md, reference files);
// it never reads or parses that content - the agent skill does.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// ProfileInfo describes one profile directory for `where`'s JSON output.
type ProfileInfo struct {
	Path       string   `json:"path"`
	Context    bool     `json:"context"`
	References []string `json:"references"`
}

// resolveProfileDirs returns the profile directories to use: explicit
// --profile-dir flags, then $STACKFORK_PROFILE_DIRS (colon-separated, ";" on
// Windows like PATH), and
// only when neither is given, every directory under
// $STACKFORK_CONFIG_DIR/profiles (default ~/.config/stackfork/profiles),
// with symlinks resolved.
func resolveProfileDirs(flagDirs []string) ([]string, error) {
	var dirs []string
	dirs = append(dirs, flagDirs...)
	if env := os.Getenv("STACKFORK_PROFILE_DIRS"); env != "" {
		dirs = append(dirs, filepath.SplitList(env)...)
	}
	if len(dirs) > 0 {
		var resolved []string
		for _, d := range dirs {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			r, err := identity.ResolvePath(d)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, r)
		}
		return resolved, nil
	}

	configDir, err := identity.ConfigDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(configDir, "profiles"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var resolved []string
	for _, e := range entries {
		p := filepath.Join(configDir, "profiles", e.Name())
		r, err := identity.ResolvePath(p)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			continue
		}
		resolved = append(resolved, r)
	}
	sort.Strings(resolved)
	return resolved, nil
}

// profileInfos lists each profile's context.md presence and reference
// filenames, without reading their content.
func profileInfos(dirs []string) []ProfileInfo {
	infos := []ProfileInfo{}
	for _, d := range dirs {
		info := ProfileInfo{Path: d, References: []string{}}
		if fi, err := os.Stat(filepath.Join(d, "context.md")); err == nil && !fi.IsDir() {
			info.Context = true
		}
		if entries, err := os.ReadDir(filepath.Join(d, "references")); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					info.References = append(info.References, e.Name())
				}
			}
			sort.Strings(info.References)
		}
		infos = append(infos, info)
	}
	return infos
}
