package prune

import (
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"7d": 168 * time.Hour, "48h": 48 * time.Hour, "2w": 336 * time.Hour, "90m": 90 * time.Minute,
	} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "abc", "-1d", "0d", "-5h", "7", "d"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0B": 0, "190MB": 190e6, "11.16kB": 11160, "1.5GB": 1.5e9, "2GiB": 2 << 30} {
		if got, ok := ParseSize(in); !ok || got != want {
			t.Errorf("%s: %d %v", in, got, ok)
		}
	}
	for _, bad := range []string{"N/A", "", "12XB"} {
		if _, ok := ParseSize(bad); ok {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestSelect(t *testing.T) {
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour)
	all := []Candidate{
		{Project: "run", Running: true, LastActivity: now},
		{Project: "stop", LastActivity: old},
		{Project: "orph", Orphaned: true, LastActivity: now},
		{Project: "orphrun", Orphaned: true, Running: true, LastActivity: now},
		{Project: "unk"},
	}
	names := func(cs []Candidate) (s string) {
		for _, c := range cs {
			s += c.Project + " "
		}
		return
	}
	for _, c := range []struct {
		s     Scopes
		older time.Duration
		want  string
	}{
		{Scopes{Orphaned: true}, 0, "orph "},
		{Scopes{Stopped: true}, 0, "stop orph unk "},
		{Scopes{All: true}, 0, "run stop orph orphrun unk "},
		{Scopes{Stopped: true}, 7 * 24 * time.Hour, "stop "},
		{Scopes{All: true}, 7 * 24 * time.Hour, "stop "},
		{Scopes{}, 0, ""},
	} {
		if got := names(Select(all, c.s, c.older, now)); got != c.want {
			t.Errorf("%+v %v: got %q want %q", c.s, c.older, got, c.want)
		}
	}
}

func TestOwnership(t *testing.T) {
	cs := []Container{
		{ID: "a", Labels: map[string]string{"io.stackfork.root": "/r", composeProjectLabel: "p"}},
		{ID: "b", Labels: map[string]string{identity.LegacyLabelRoot: "/r", composeProjectLabel: "p"}},
		{ID: "c", Labels: map[string]string{composeProjectLabel: "p"}},                             // no stackfork label
		{ID: "d", Labels: map[string]string{"io.stackfork.root": "/r", composeProjectLabel: "p2"}}, // other project
		{ID: "e", Labels: map[string]string{"io.stackfork.root": "/r", composeProjectLabel: "p-x"}},
	}
	got := ownedContainers(cs, "p")
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("%+v", got)
	}
}

// A leftover or running one-off `compose run` container carries the project
// label, so pruning an instance removes it with the rest.
func TestOneOffContainersAreOwned(t *testing.T) {
	cs := []Container{
		{ID: "svc", Labels: map[string]string{"io.stackfork.root": "/r", composeProjectLabel: "p"}},
		{ID: "run", Labels: map[string]string{"io.stackfork.root": "/r", composeProjectLabel: "p", "com.docker.compose.oneoff": "True"}},
	}
	if got := ownedContainers(cs, "p"); len(got) != 2 || got[1].ID != "run" {
		t.Fatalf("%+v", got)
	}
}
