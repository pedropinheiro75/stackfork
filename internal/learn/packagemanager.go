package learn

import "path/filepath"

// lockfiles maps a lockfile name to the package manager it identifies,
// checked in order (task 13.2: "bun, pnpm, yarn, npm").
var lockfiles = []struct{ file, manager string }{
	{"bun.lockb", "bun"},
	{"bun.lock", "bun"},
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"package-lock.json", "npm"},
}

// otherManifests are recorded as detection-only (task 13.2): present or
// not, never parsed for scripts or versions.
var otherManifests = []string{"go.mod", "requirements.txt", "pyproject.toml", "Gemfile"}

// detectPackageManager reports the JS package manager and its lockfile, and
// any other language manifest files found at the root (Go, Python, Ruby).
func detectPackageManager(root string) (manager, lockfile string, others []string) {
	for _, c := range lockfiles {
		if fileExists(filepath.Join(root, c.file)) {
			manager, lockfile = c.manager, c.file
			break
		}
	}
	for _, name := range otherManifests {
		if fileExists(filepath.Join(root, name)) {
			others = append(others, name)
		}
	}
	return manager, lockfile, others
}
