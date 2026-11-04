package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	info := func(v string, ok bool) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: v}}, ok }
	}
	pseudo := "v0.1.15-0.20261104023000-abcdef123456"
	for _, c := range []struct {
		stamped string
		read    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"0.2.0", info(pseudo, true), "0.2.0"}, // ldflags win
		{"dev", info("v0.2.0", true), "v0.2.0"},
		{"", info(pseudo, true), pseudo},
		{"dev", info("(devel)", true), "dev"},
		{"dev", info("", true), "dev"},
		{"dev", info(pseudo, false), "dev"},
	} {
		if got := resolveVersion(c.stamped, c.read); got != c.want {
			t.Errorf("resolveVersion(%q) = %q, want %q", c.stamped, got, c.want)
		}
	}
}
