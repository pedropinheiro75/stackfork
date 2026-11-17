package repos

import (
	"os"
	"path/filepath"
	"strings"
)

// Ecosystems of the setup prompt. Detection is by project files only.
const (
	ecoGo = "go"
	ecoJS = "js"
)

// scanContainers are folders whose children are often separate projects of a
// monorepo.
var scanContainers = []string{"apps", "packages", "services", "cmd"}

// detectEcosystems returns the ecosystems found in root, its direct
// subfolders and the subfolders of apps, packages, services and cmd, in a
// fixed order (go, js). It returns nothing when no known file is found: the
// prompt then keeps the base text only.
func detectEcosystems(root string) []string {
	found := map[string]bool{}
	check := func(dir string) {
		if fileExists(filepath.Join(dir, "go.mod")) {
			found[ecoGo] = true
		}
		if fileExists(filepath.Join(dir, "package.json")) {
			found[ecoJS] = true
		}
	}
	check(root)
	for _, d := range childDirs(root) {
		check(d)
	}
	for _, c := range scanContainers {
		for _, d := range childDirs(filepath.Join(root, c)) {
			check(d)
		}
	}
	var out []string
	for _, e := range []string{ecoGo, ecoJS} {
		if found[e] {
			out = append(out, e)
		}
	}
	return out
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// childDirs lists the visible subfolders of dir, without dependency folders.
func childDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" {
			continue
		}
		out = append(out, filepath.Join(dir, n))
	}
	return out
}

const basePrompt = "Check which setup tasks this project has: installing dependencies, database migrations, seed data and a login. " +
	"Record a step only for a task the project really has, and say in a note which ones it does not. " +
	"Before you record a migrate or seed step, read the startup findings in `.stackfork/knowledge.md` and the first-start logs: if the container already migrates or seeds on start, do not record that step (say so in a note), and a seed step must not repeat the migrate step. " +
	"Read the README, the docs and the Compose file for the local URLs that matter (the app, an admin or dashboard page, a mail UI, API docs, Storybook), check that each one answers, and record them with `stackfork learn record entry` and `learn record link`. " +
	"If the project needs a credential stackfork cannot find (a private registry token, an API key), record its name with `stackfork learn record secret NAME --hint TEXT` and ask me to set it; never ask me to paste a value. " +
	"Confirm hot reload: edit a visible string, check it changes without a restart, then revert the edit, and record the result with `stackfork learn record reload SERVICE hot|restart|rebuild`. " +
	"Report `stackfork urls`, and once I confirm it works record it with `stackfork learn verify`. "

var ecosystemPrompts = map[string]string{
	ecoGo: "Go project: record `go mod download` as an exec step with `--once deps` only if the build runs on the host or the image does not already download modules. Find how the service starts (`go run`, a built binary, or a reload tool such as air) in the Makefile, Compose file or Dockerfile. Migrations and seed data exist only if the repo has a migrations folder, a migrate tool or a seed command; do not invent them. ",
	ecoJS: "JavaScript or TypeScript project: record the install command (`yarn install --frozen-lockfile`, `npm ci`, `pnpm install --frozen-lockfile` or `bun install`, as the lockfile says) as an exec step with `--once deps` (`--on host`, or in the container), before the build and migrate steps: it runs again when a lockfile changes or `node_modules` is missing. Look in the `package.json` scripts for migrate, seed and dev commands. A login is needed only if the app has one. ",
}

// setupGuidance is the part of the setup prompt after the first sentence: the
// ecosystem sections for the detected project files, then the base text. A
// mixed project gets several sections; an inconclusive detection gets none.
func setupGuidance(root string) string {
	var b strings.Builder
	for _, e := range detectEcosystems(root) {
		b.WriteString(ecosystemPrompts[e])
	}
	b.WriteString(basePrompt)
	b.WriteString("Ask before building anything beyond the running instance.")
	return b.String()
}
