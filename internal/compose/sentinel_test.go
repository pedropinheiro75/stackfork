package compose

import (
	"strings"
	"testing"
)

// windowsChild is the environment a child gets on Windows: os/exec keeps the
// last of names that differ only by case, and a lookup ignores case.
func windowsChild(env []string) func(string) string {
	vals := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		vals[strings.ToUpper(k)] = v
	}
	return func(name string) string { return vals[strings.ToUpper(name)] }
}

// On Windows, npm_token and its alias NPM_TOKEN are one variable. A source
// `${NPM_TOKEN}` must come back as `${NPM_TOKEN}` every time, whatever order
// the map gives (the intermittent test-windows failure).
func TestSecretSentinelsFoldCaseOnWindows(t *testing.T) {
	for i := 0; i < 50; i++ {
		sentinels := secretSentinels([]string{"npm_token"}, true)
		got := windowsChild(renderEnv("", sentinels))("NPM_TOKEN")
		for name, sentinel := range sentinels {
			got = strings.ReplaceAll(got, sentinel, "${"+name+"}")
		}
		if got != "${NPM_TOKEN}" {
			t.Fatalf("run %d: ${NPM_TOKEN} came back as %q (sentinels %v)", i, got, sentinels)
		}
	}
	// names are case-sensitive elsewhere: both spellings keep their own sentinel
	if s := secretSentinels([]string{"npm_token"}, false); len(s) != 2 || s["npm_token"] == s["NPM_TOKEN"] {
		t.Fatalf("case-sensitive sentinels: %v", s)
	}
}
