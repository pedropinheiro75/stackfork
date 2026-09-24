package learn

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// purposeKeywords is checked in order, so a key matching more than one
// group (e.g. AWS_SECRET_ACCESS_KEY: "storage" and "auth") lands in the
// first, more specific one.
var purposeKeywords = []struct {
	purpose  string
	keywords []string
}{
	{knowledge.PurposeRegistry, []string{"NPM", "REGISTRY"}},
	{knowledge.PurposeDatabase, []string{"DATABASE", "POSTGRES", "MYSQL", "MARIADB", "MONGO", "REDIS", "DB_", "_DB"}},
	{knowledge.PurposeMail, []string{"MAIL", "SMTP", "SENDGRID", "EMAIL"}},
	{knowledge.PurposeStorage, []string{"S3", "BUCKET", "STORAGE", "AWS", "BLOB", "CDN"}},
	{knowledge.PurposeAuth, []string{"AUTH", "JWT", "SECRET", "TOKEN", "SESSION", "OAUTH", "PASSWORD", "CLIENT_ID"}},
}

func inferPurpose(name string) string {
	upper := strings.ToUpper(name)
	for _, group := range purposeKeywords {
		for _, kw := range group.keywords {
			if strings.Contains(upper, kw) {
				return group.purpose
			}
		}
	}
	return knowledge.PurposeOther
}

// parseEnvKeyNames extracts KEY from each KEY=VALUE line, discarding VALUE
// entirely: task 13.4 requires names only, never a value, from any env
// file, including a real .env with real secrets in it.
func parseEnvKeyNames(data []byte) []string {
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "export ")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		names = append(names, key)
	}
	return names
}

// detectEnvKeys reads .env.example/.env.sample plus the env files the
// rendered Compose config references (envFiles, already resolved absolute
// paths confirmed to be inside root by the caller), recording each
// variable's NAME and inferred purpose only.
func detectEnvKeys(root string, envFiles []string) []knowledge.EnvKey {
	var keys []knowledge.EnvKey
	seen := map[string]bool{}
	add := func(name, source string) {
		id := name + "|" + source
		if name == "" || seen[id] {
			return
		}
		seen[id] = true
		keys = append(keys, knowledge.EnvKey{Name: name, Purpose: inferPurpose(name), Source: source})
	}

	for _, candidate := range []string{".env.example", ".env.sample"} {
		if data, ok := readFileNoSymlink(filepath.Join(root, candidate)); ok {
			for _, name := range parseEnvKeyNames(data) {
				add(name, candidate)
			}
		}
	}
	for _, ef := range envFiles {
		data, ok := readFileNoSymlink(ef)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(root, ef)
		if err != nil {
			continue
		}
		for _, name := range parseEnvKeyNames(data) {
			add(name, "env_file:"+filepath.ToSlash(rel))
		}
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Purpose != keys[j].Purpose {
			return keys[i].Purpose < keys[j].Purpose
		}
		return keys[i].Name < keys[j].Name
	})
	return keys
}
