package learn

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// reloadWatcher matches a command that watches the source and reloads.
var reloadWatcher = regexp.MustCompile(`(?i)\b(vite|nodemon|ts-node-dev|webpack-dev-server|webpack\s+serve|(next|nuxt|nuxi|astro|remix|storybook)\s+dev|react-scripts\s+start|ng\s+serve|tsx\s+(watch|-w)|(bun|node)\s+(run\s+)?--(hot|watch)|uvicorn\b.*--reload|gunicorn\b.*--reload|flask\b.*--(debug|reload)|manage\.py\s+runserver|rails\s+(s|server)|air|watchexec|cargo\s+watch|CompileDaemon|modd)\b`)

// reloadScriptRef finds `npm run dev`, `yarn dev`, `pnpm start` ...
var reloadScriptRef = regexp.MustCompile(`\b(?:npm|yarn|pnpm|bun)(?:\s+run)?\s+([\w:.-]+)`)

// isWatcher says whether cmd, or a package.json script it runs, is a watcher,
// with the package.json line when the script decided it.
func isWatcher(root string, scripts []knowledge.Script, cmd string) (bool, string) {
	if reloadWatcher.MatchString(cmd) {
		return true, ""
	}
	for _, m := range reloadScriptRef.FindAllStringSubmatch(cmd, -1) {
		for _, s := range scripts {
			if s.Source == "package.json" && s.Name == m[1] && reloadWatcher.MatchString(s.Command) {
				return true, whereIn(root, "package.json", `"`+s.Name+`"`)
			}
		}
	}
	return false, ""
}

// whereIn is "file:line" of the first line of rel holding needle (just the
// file when it is not found).
func whereIn(root, rel, needle string) string {
	if data, ok := readFileNoSymlink(filepath.Join(root, rel)); ok {
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, needle) {
				return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), i+1)
			}
		}
	}
	return filepath.ToSlash(rel)
}

// proposeReload guesses, per app service and host step, how an edit reaches
// the running app. A command the scan cannot read gives no entry (unknown).
// ponytail: names and patterns only; a watcher hidden in a shell script or a
// Makefile target reads as "restart" until someone records it.
func proposeReload(root string, rendered interface{}, composeFiles []string, k *knowledge.Knowledge) []knowledge.Reload {
	model, _ := rendered.(map[string]interface{})
	svcs, _ := model["services"].(map[string]interface{})
	var out []knowledge.Reload
	for _, s := range k.Facts.Services {
		raw, _ := svcs[s.Name].(map[string]interface{})
		if raw == nil {
			continue
		}
		mounted := false
		for _, v := range s.Volumes {
			if src, ok := strings.CutPrefix(v, "bind:"); ok && underRoot(src, root) {
				mounted = true
			}
		}
		if !s.Build && !mounted {
			continue // an off-the-shelf image (a database, a cache)
		}
		cmd := strings.TrimSpace(composeText(raw["entrypoint"]) + " " + composeText(raw["command"]))
		dfEntry, dfCmd := dockerfileStart(root, raw["build"])
		if cmd == "" {
			for _, u := range []*startupUnit{dfEntry, dfCmd} {
				if u != nil {
					cmd += " " + u.text
				}
			}
		}
		r := knowledge.Reload{Service: s.Name, Proposed: true}
		switch hot, line := isWatcher(root, k.Facts.Scripts, cmd); {
		case !mounted && s.Build:
			r.Mode = knowledge.ReloadRebuild
			r.Evidence = composeEvidence(root, composeFiles, s.Name)
			if df, rel, ok := dockerfilePath(root, raw["build"]); ok {
				if data, ok := readFileNoSymlink(df); ok {
					for i, l := range strings.Split(string(data), "\n") {
						if f := strings.Fields(l); len(f) > 1 && (strings.EqualFold(f[0], "COPY") || strings.EqualFold(f[0], "ADD")) && !strings.HasPrefix(f[1], "--from") {
							r.Evidence = fmt.Sprintf("%s:%d", filepath.ToSlash(rel), i+1)
							break
						}
					}
				}
			}
		case mounted && hot:
			r.Mode, r.Evidence = knowledge.ReloadHot, line
		case mounted && strings.TrimSpace(cmd) != "":
			r.Mode = knowledge.ReloadRestart
		default:
			continue
		}
		if r.Evidence == "" {
			r.Evidence = composeEvidence(root, composeFiles, s.Name)
		}
		out = append(out, r)
	}
	return out
}

// proposeHostReload covers the plan's host steps, which always run on the host.
func proposeHostReload(root string, k *knowledge.Knowledge) []knowledge.Reload {
	var out []knowledge.Reload
	for _, st := range k.Plan.HostSteps() {
		if st.Name == "" || st.Cmd == "" {
			continue
		}
		r := knowledge.Reload{Service: st.Name, Mode: knowledge.ReloadRestart, Proposed: true}
		if hot, line := isWatcher(root, k.Facts.Scripts, st.Cmd); hot {
			r.Mode, r.Evidence = knowledge.ReloadHot, line
		}
		out = append(out, r)
	}
	return out
}

// composeEvidence is the Compose line that declares the service.
func composeEvidence(root string, composeFiles []string, name string) string {
	for _, f := range composeFiles {
		if data, err := os.ReadFile(filepath.Join(root, f)); err == nil {
			for i, line := range strings.Split(string(data), "\n") {
				if strings.TrimSpace(line) == name+":" {
					return fmt.Sprintf("%s:%d", f, i+1)
				}
			}
		}
	}
	if len(composeFiles) > 0 {
		return composeFiles[0]
	}
	return ""
}
