package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opens a real terminal window running a stub agent. Run by hand:
// STACKFORK_LIVE_TERMINAL=warp|iterm|terminal go test -run LiveOpenAgentTerminal ./internal/dashboard
func TestLiveOpenAgentTerminal(t *testing.T) {
	term := os.Getenv("STACKFORK_LIVE_TERMINAL")
	if term == "" {
		t.Skip("set STACKFORK_LIVE_TERMINAL to open a real window")
	}
	dir, state := t.TempDir(), t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	stub := filepath.Join(dir, "fake-agent")
	os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s' \"$1\" > "+marker+"\necho 'stackfork test: close this window'\n"), 0o755)
	if err := OpenAgentTerminal(state, term, dir, agentDef{ID: "x", Name: "Stub", Bin: stub}, "it's a `learn` prompt"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if b, err := os.ReadFile(marker); err == nil {
			if string(b) != "it's a `learn` prompt" {
				t.Fatalf("prompt arrived as %q", b)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	b, _ := os.ReadFile(marker)
	t.Fatalf("the agent never ran in %s (%s)", term, strings.TrimSpace(string(b)))
}
