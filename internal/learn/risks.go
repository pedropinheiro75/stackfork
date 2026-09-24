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
