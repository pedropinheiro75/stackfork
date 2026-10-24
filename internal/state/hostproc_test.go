package state

import "testing"

func TestRestartArgsShape(t *testing.T) {
	ok := []string{"stackfork", "--state-dir", "/s", "--instance", "p", "host", "--supervise", "api", "--port", "1", "--", "bun", "dev"}
	if got := (HostProc{Cmdline: ok}).RestartArgs(); len(got) != len(ok)-1 {
		t.Errorf("a supervisor command line restarts: %v", got)
	}
	r := []string{"--instance", "p", "host", "--supervise", "api", "--", "bun", "dev"}
	if got := (HostProc{Restart: r}).RestartArgs(); len(got) != len(r) {
		t.Errorf("recorded restart args: %v", got)
	}
	for name, bad := range map[string][]string{
		"no host before --supervise": {"--supervise", "api", "--", "x"},
		"no name":                    {"host", "--supervise", "--", "x"},
		"bad name":                   {"host", "--supervise", "../x", "--", "x"},
		"no command":                 {"host", "--supervise", "api"},
		"not a host call":            {"dashboard", "--port", "1", "--", "rm", "-rf", "/"},
	} {
		if got := (HostProc{Restart: bad}).RestartArgs(); got != nil {
			t.Errorf("%s: %v must not run", name, got)
		}
		if got := (HostProc{Cmdline: append([]string{"stackfork"}, bad...)}).RestartArgs(); got != nil {
			t.Errorf("%s (cmdline): %v must not run", name, got)
		}
	}
}
