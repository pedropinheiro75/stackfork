package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func lsSources(t *testing.T, args ...string) map[string]string {
	t.Helper()
	out, e, code := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	var rows []struct{ Name, Reference, Source string }
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Source
	}
	return got
}

// Review M5 (D10): secret ls shows where each reference comes from: a flag
// of this command, the map, the repository's own instance (manifest) or
// another worktree's instance (sibling).
func TestSecretLsShowsFlagAndSiblingSources(t *testing.T) {
	repo, _, base := siblingRepo(t) // only the worktree feat-a is prepared, with tok
	t.Setenv("SF_TEST_LS_FLAG", "x")
	mapSecret(t, repo, "MAPPED", "env:SF_TEST_LS_MAPPED")
	got := lsSources(t, append(append([]string{}, base...), "--secret", "FLAGGED=env:SF_TEST_LS_FLAG", "secret", "ls", repo, "--json")...)
	for name, want := range map[string]string{"tok": "sibling", "MAPPED": "map", "FLAGGED": "flag"} {
		if got[name] != want {
			t.Errorf("%s: source %q, want %q (%v)", name, got[name], want, got)
		}
	}
	// once the repository's own checkout is prepared with it, the source is manifest
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", repo, "--secret", "tok=env:"+sibTokenName,
		"prepare", "-f", "docker-compose.yml")...); code != 0 {
		t.Fatal(e)
	}
	if got := lsSources(t, append(append([]string{}, base...), "secret", "ls", repo, "--json")...); got["tok"] != "manifest" {
		t.Errorf("own instance: %v", got)
	}
	// a flag that is not env:/npmrc: is refused here too
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--secret", "X=keychain:other", "secret", "ls", repo)...); code == 0 || !strings.Contains(e, "secret map") {
		t.Errorf("%d %s", code, e)
	}
}
