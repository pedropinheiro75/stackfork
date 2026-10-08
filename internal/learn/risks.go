package learn

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// Risk kinds (task 13.4).
const (
	riskResetSeedOnStart  = "reset_or_seed_on_start"
	riskWritesEnvOrNpmrc  = "writes_env_or_npmrc"
	riskHardcodedComposeP = "hardcoded_compose_project"
	riskHardcodedExec     = "hardcoded_docker_exec"
	riskUnpinnedToolchain = "unpinned_toolchain"
)

var (
	// resetSeedPattern flags an unconditional reset/seed/destructive command,
	// checked only against entrypoints (which run on every container start)
	// and dev/start scripts (which run on every `up`/dev invocation) - never
	// against a script or target *named* reset/seed, which is supposed to
	// contain one.
	resetSeedPattern = regexp.MustCompile(`(?i)\b(migrate\s+reset|db\s*:?\s*reset|drop\s+schema|drop\s+database|truncate|flush\s*all|reset\s*--force|seed)\b`)

	// envWritePattern flags a redirect into .env/.npmrc (or a variant such
	// as .env.local), never a read of one.
	envWritePattern = regexp.MustCompile(`>{1,2}\s*['"]?(\.env(?:\.[A-Za-z0-9_.-]+)?|\.npmrc)\b`)

	// composeProjectPattern flags `docker compose -p <fixed-name>`; a
	// variable reference ($NAME, ${NAME}) is not fixed and is not flagged.
	composeProjectPattern = regexp.MustCompile(`docker\s+compose\s+(?:--\S+\s+|-[a-zA-Z]\s+\S+\s+)*-p\s+([^\s$][^\s]*)`)

	// globalInstallPattern captures the package list of `npm i -g ...`,
	// `pnpm add --global ...`, `yarn global add ...`, `bun add -g ...`.
	globalInstallPattern = regexp.MustCompile(`\b(?:npm|pnpm|yarn|bun)\s+(?:global\s+)?(?:install|i|add)\b([^&|;]*)`)

	globalFlagPattern = regexp.MustCompile(`(^|\s)(-g|--global)\b|yarn\s+global`)

	// curlPipePattern flags `curl ... | bash` style installers.
	curlPipePattern = regexp.MustCompile(`\b(?:curl|wget)\b[^|]*\|\s*(?:sudo\s+)?(?:ba|z)?sh\b`)

	// versionLikePattern: a pinned version somewhere on the line.
	versionLikePattern = regexp.MustCompile(`(?i)\bv?\d+\.\d+|VERSION|@\d`)

	// dockerExecPattern flags `docker exec [-flags] <fixed-name>`.
	dockerExecPattern = regexp.MustCompile(`docker\s+exec\s+(?:-\w+\s+)*([^\s$-][^\s]*)`)
)

// scriptLine is one command line to check, with the location it came from.
type scriptLine struct {
	file    string
	line    int
	text    string
	onStart bool // true for an entrypoint, or a dev/start-role script
}

func detectRisks(root string) []knowledge.Risk {
	var lines []scriptLine

	lines = append(lines, entrypointLines(root)...)
	lines = append(lines, packageJSONScriptLines(root)...)
	lines = append(lines, makeTargetLines(root, "Makefile")...)
	lines = append(lines, makeTargetLines(root, "justfile")...)

	var risks []knowledge.Risk
	risks = append(risks, unpinnedToolchains(root)...)
	for _, l := range lines {
		if l.onStart {
			if m := resetSeedPattern.FindString(l.text); m != "" {
				risks = append(risks, knowledge.Risk{Kind: riskResetSeedOnStart, File: l.file, Line: l.line,
					Detail: "runs on every start: " + strings.TrimSpace(l.text)})
			}
		}
		if m := envWritePattern.FindStringSubmatch(l.text); m != nil {
			risks = append(risks, knowledge.Risk{Kind: riskWritesEnvOrNpmrc, File: l.file, Line: l.line,
				Detail: "writes " + m[1] + ": " + strings.TrimSpace(l.text)})
		}
		if m := composeProjectPattern.FindStringSubmatch(l.text); m != nil {
			risks = append(risks, knowledge.Risk{Kind: riskHardcodedComposeP, File: l.file, Line: l.line,
				Detail: "hardcoded Compose project " + m[1] + ": " + strings.TrimSpace(l.text)})
		}
		if m := dockerExecPattern.FindStringSubmatch(l.text); m != nil {
			risks = append(risks, knowledge.Risk{Kind: riskHardcodedExec, File: l.file, Line: l.line,
				Detail: "hardcoded container name " + m[1] + ": " + strings.TrimSpace(l.text)})
		}
	}
	return risks
}

func entrypointLines(root string) []scriptLine {
	var lines []scriptLine
	for _, rel := range findByPredicate(root, isEntrypointName) {
		data, ok := readFileNoSymlink(filepath.Join(root, rel))
		if !ok {
			continue
		}
		for i, text := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(text) == "" {
				continue
			}
			lines = append(lines, scriptLine{file: rel, line: i + 1, text: text, onStart: true})
		}
	}
	return lines
}

func packageJSONScriptLines(root string) []scriptLine {
	raw, ok := readPackageJSONScripts(root)
	if !ok {
		return nil
	}
	data, _ := readFileNoSymlink(filepath.Join(root, "package.json"))
	var lines []scriptLine
	for name, command := range raw {
		role, _ := scriptRole(name)
		lines = append(lines, scriptLine{
			file:    "package.json",
			line:    lineOfKey(data, `"`+name+`"`),
			text:    command,
			onStart: role == "dev",
		})
	}
	return lines
}

func makeTargetLines(root, filename string) []scriptLine {
	data, ok := readFileNoSymlink(filepath.Join(root, filename))
	if !ok {
		return nil
	}
	var lines []scriptLine
	fileLines := strings.Split(string(data), "\n")
	for i, line := range fileLines {
		m := makeTarget.FindStringSubmatch(line)
		if m == nil || m[1] == ".PHONY" {
			continue
		}
		role, _ := scriptRole(m[1])
		// Recipe body: subsequent indented lines, until a blank or
		// non-indented one.
		for j := i + 1; j < len(fileLines); j++ {
			body := fileLines[j]
			if !strings.HasPrefix(body, "\t") && !strings.HasPrefix(body, "    ") {
				break
			}
			lines = append(lines, scriptLine{file: filename, line: j + 1, text: body, onStart: role == "dev"})
		}
	}
	return lines
}

const pinHint = "pin a version; a newer tool can reject the lockfile"

// unpinnedToolchains flags global toolchain installs and floating base
// images in Dockerfiles and entrypoints.
func unpinnedToolchains(root string) []knowledge.Risk {
	isDockerfile := func(n string) bool {
		l := strings.ToLower(n)
		return strings.HasPrefix(l, "dockerfile") || strings.HasSuffix(l, ".dockerfile")
	}
	files := append(findByPredicate(root, isDockerfile), findByPredicate(root, isEntrypointName)...)
	var risks []knowledge.Risk
	flag := func(rel string, n int, what, text string) {
		risks = append(risks, knowledge.Risk{Kind: riskUnpinnedToolchain, File: rel, Line: n,
			Detail: what + ": " + strings.TrimSpace(text) + " (" + pinHint + ")"})
	}
	for _, rel := range files {
		data, ok := readFileNoSymlink(filepath.Join(root, rel))
		if !ok {
			continue
		}
		stages := map[string]bool{}
		for i, text := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(text)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			f := strings.Fields(t)
			if strings.EqualFold(f[0], "FROM") {
				var img string
				for _, a := range f[1:] {
					if !strings.HasPrefix(a, "--") {
						img = a
						break
					}
				}
				name := strings.ToLower(img)
				last := name[strings.LastIndex(name, "/")+1:]
				switch {
				case img == "" || name == "scratch" || strings.Contains(img, "$") || stages[name] || strings.Contains(img, "@sha256:"):
				case !strings.Contains(last, ":"):
					flag(rel, i+1, "base image without a tag", t)
				case strings.HasSuffix(name, ":latest"):
					flag(rel, i+1, "base image on :latest", t)
				}
				if len(f) >= 3 && strings.EqualFold(f[len(f)-2], "AS") {
					stages[strings.ToLower(f[len(f)-1])] = true
				}
				continue
			}
			if m := globalInstallPattern.FindStringSubmatch(t); m != nil && globalFlagPattern.MatchString(t) {
				for _, a := range strings.Fields(m[1]) {
					if strings.HasPrefix(a, "-") || strings.Contains(a, "$") {
						continue
					}
					if !strings.Contains(a[1:], "@") && a != "global" {
						flag(rel, i+1, "global install of "+a+" without a version", t)
					}
				}
			}
			if curlPipePattern.MatchString(t) && !versionLikePattern.MatchString(t) {
				flag(rel, i+1, "installer piped to a shell without a version", t)
			}
		}
	}
	return risks
}
