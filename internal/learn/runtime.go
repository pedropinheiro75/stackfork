package learn

import (
	"path/filepath"
	"regexp"
	"strings"
)

var goDirective = regexp.MustCompile(`(?m)^go\s+(\S+)`)

// detectRuntimeVersions reads the version files task 13.2 lists
// (.node-version, .nvmrc, .tool-versions, .bun-version, go.mod's `go`
// directive) and returns a runtime name -> version map.
func detectRuntimeVersions(root string) map[string]string {
	versions := map[string]string{}

	if v, ok := readTrimmed(root, ".node-version"); ok {
		versions["node"] = strings.TrimPrefix(v, "v")
	} else if v, ok := readTrimmed(root, ".nvmrc"); ok {
		versions["node"] = strings.TrimPrefix(v, "v")
	}
	if v, ok := readTrimmed(root, ".bun-version"); ok {
		versions["bun"] = strings.TrimPrefix(v, "v")
	}
	if data, ok := readFileNoSymlink(filepath.Join(root, ".tool-versions")); ok {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				versions[fields[0]] = fields[1]
			}
		}
	}
	if data, ok := readFileNoSymlink(filepath.Join(root, "go.mod")); ok {
		if m := goDirective.FindSubmatch(data); m != nil {
			versions["go"] = string(m[1])
		}
	}
	return versions
}

func readTrimmed(root, name string) (string, bool) {
	data, ok := readFileNoSymlink(filepath.Join(root, name))
	if !ok {
		return "", false
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", false
	}
	return v, true
}
