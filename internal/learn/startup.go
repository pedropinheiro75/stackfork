package learn

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

var (
	startupMigratePattern = regexp.MustCompile(`(?i)\bmigrate\b|\balembic\s+upgrade\b|\bmigration:run\b|\bdb\s*:?\s*push\b`)
	startupSeedPattern    = regexp.MustCompile(`(?i)\bseed\b`)
)

// maxScriptDepth bounds how many scripts deep an entrypoint is followed.
const maxScriptDepth = 3

// startupUnit is one piece of a service's start: a Compose entrypoint or
// command (file is "" until a line is found), or a Dockerfile instruction or
// script line.
type startupUnit struct {
	file string
	line int
	text string
}

// annotateStartup sets StartupMigrate/StartupSeed on each scanned service
// whose effective start runs one: the Compose entrypoint/command, else the
// Dockerfile ENTRYPOINT/CMD it builds, and the scripts either one runs.
// ponytail: a command is found by text, not run through a shell, so a
// conditional migrate still counts; Compose `extends` and an `include`d build
// are read only as Compose renders them.
func annotateStartup(root string, rendered interface{}, composeFiles []string, services []knowledge.Service) {
	model, _ := rendered.(map[string]interface{})
	svcs, _ := model["services"].(map[string]interface{})
	byBase := map[string][]string{} // script base name -> root-relative paths
	for i := range services {
		raw, _ := svcs[services[i].Name].(map[string]interface{})
		if raw == nil {
			continue
		}
		var units []startupUnit
		entry, cmd := composeText(raw["entrypoint"]), composeText(raw["command"])
		dfEntry, dfCmd := dockerfileStart(root, raw["build"])
		if entry != "" {
			units = append(units, startupUnit{text: entry})
		} else if dfEntry != nil {
			units = append(units, *dfEntry)
		}
		if cmd != "" {
			units = append(units, startupUnit{text: cmd})
		} else if entry == "" && dfCmd != nil { // a Compose entrypoint resets the image CMD
			units = append(units, *dfCmd)
		}
		for _, u := range units {
			scanStartup(root, composeFiles, byBase, u, map[string]bool{}, 0, &services[i])
		}
	}
}

func scanStartup(root string, composeFiles []string, byBase map[string][]string, u startupUnit, seen map[string]bool, depth int, svc *knowledge.Service) {
	if svc.StartupMigrate == nil {
		if m := startupMigratePattern.FindString(u.text); m != "" {
			svc.StartupMigrate = locate(composeFiles, root, u, m)
		}
	}
	if svc.StartupSeed == nil {
		if m := startupSeedPattern.FindString(u.text); m != "" {
			svc.StartupSeed = locate(composeFiles, root, u, m)
		}
	}
	if depth >= maxScriptDepth {
		return
	}
	for _, f := range strings.Fields(u.text) {
		f = strings.Trim(f, `"';&|()`)
		base := path.Base(filepath.ToSlash(f))
		if !strings.HasSuffix(base, ".sh") && !strings.HasSuffix(base, ".bash") && !isEntrypointName(base) {
			continue
		}
		if _, ok := byBase[base]; !ok {
			byBase[base] = findByPredicate(root, func(n string) bool { return n == base })
		}
		for _, rel := range byBase[base] {
			if seen[rel] {
				continue
			}
			seen[rel] = true
			data, ok := readFileNoSymlink(filepath.Join(root, rel))
			if !ok {
				continue
			}
			for i, line := range strings.Split(string(data), "\n") {
				if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
					scanStartup(root, composeFiles, byBase, startupUnit{file: filepath.ToSlash(rel), line: i + 1, text: t}, seen, depth+1, svc)
				}
			}
		}
	}
}

// locate makes the StartupRun for a match in u; a Compose unit has no line
// yet, so the first Compose line holding the matched word gives it.
func locate(composeFiles []string, root string, u startupUnit, match string) *knowledge.StartupRun {
	r := &knowledge.StartupRun{File: u.file, Line: u.line, Cmd: truncate(strings.TrimSpace(u.text), 120)}
	if r.File != "" {
		return r
	}
	for _, f := range composeFiles {
		data, ok := readFileNoSymlink(filepath.Join(root, f))
		if !ok {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(strings.ToLower(line), strings.ToLower(match)) {
				r.File, r.Line = f, i+1
				return r
			}
		}
	}
	if len(composeFiles) > 0 {
		r.File = composeFiles[0]
	}
	return r
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// composeText is a Compose entrypoint/command (string or list) as one line.
func composeText(v interface{}) string {
	switch c := v.(type) {
	case string:
		return c
	case []interface{}:
		return strings.Join(stringSlice(c), " ")
	}
	return ""
}

var dockerfileJSONPunct = strings.NewReplacer("[", " ", "]", " ", `"`, " ", ",", " ")

// dockerfilePath resolves the Dockerfile a Compose build names (absolute, and
// relative to root) when it is a plain file inside the checkout.
func dockerfilePath(root string, build interface{}) (df, rel string, ok bool) {
	var ctx string
	switch b := build.(type) {
	case string:
		ctx = b
	case map[string]interface{}:
		ctx, _ = b["context"].(string)
		df, _ = b["dockerfile"].(string)
	default:
		return "", "", false
	}
	if !filepath.IsAbs(ctx) {
		ctx = filepath.Join(root, ctx)
	}
	if df == "" {
		df = "Dockerfile"
	}
	if !filepath.IsAbs(df) {
		df = filepath.Join(ctx, df)
	}
	rel, err := filepath.Rel(root, df)
	if err != nil || !filepath.IsLocal(rel) {
		return "", "", false
	}
	return df, rel, true
}

// dockerfileStart returns the ENTRYPOINT and CMD of the last stage of the
// Dockerfile a Compose build names, nil for each that it does not set.
func dockerfileStart(root string, build interface{}) (entry, cmd *startupUnit) {
	df, rel, ok := dockerfilePath(root, build)
	if !ok {
		return nil, nil
	}
	data, ok := readFileNoSymlink(df)
	if !ok {
		return nil, nil
	}
	for i, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		u := &startupUnit{file: filepath.ToSlash(rel), line: i + 1, text: dockerfileJSONPunct.Replace(strings.Join(f[1:], " "))}
		switch strings.ToUpper(f[0]) {
		case "FROM":
			entry, cmd = nil, nil // a new stage starts clean
		case "ENTRYPOINT":
			entry, cmd = u, nil // a new ENTRYPOINT resets CMD
		case "CMD":
			cmd = u
		}
	}
	return entry, cmd
}

// EntrypointRuns is what the app service's own start already migrates and
// seeds, from the scan (nil when it does not, or no app service is known).
func EntrypointRuns(k *knowledge.Knowledge) (migrate, seed *knowledge.StartupRun) {
	if k == nil {
		return nil, nil
	}
	if s := svcNamed(k.Facts.Services, appService(k)); s != nil {
		return s.StartupMigrate, s.StartupSeed
	}
	return nil, nil
}

func svcNamed(services []knowledge.Service, name string) *knowledge.Service {
	for i := range services {
		if services[i].Name == name {
			return &services[i]
		}
	}
	return nil
}
