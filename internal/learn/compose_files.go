package learn

import (
	"path/filepath"
	"sort"
)

// baseComposeNames is the discovery order Compose itself uses for the
// implicit base file (task 13.2).
var baseComposeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// findComposeFiles returns the project's Compose files relative to root:
// the first base file found, plus any compose.*.yaml/compose.*.yml override
// sitting next to it, sorted. Empty when the project has no Compose file at
// all (task 13.2: "works for a project with no Compose").
func findComposeFiles(root string) []string {
	var base string
	for _, name := range baseComposeNames {
		if fileExists(filepath.Join(root, name)) {
			base = name
			break
		}
	}
	if base == "" {
		return nil
	}
	files := []string{base}

	var overrides []string
	for _, pattern := range []string{"compose.*.yaml", "compose.*.yml"} {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			rel := filepath.Base(m)
			if rel == base {
				continue
			}
			overrides = append(overrides, rel)
		}
	}
	sort.Strings(overrides)
	return append(files, overrides...)
}
