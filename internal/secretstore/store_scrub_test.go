package secretstore

import (
	"strings"
	"testing"
)

// Review M2: a store tool can echo the command it was given. Store never
// returns an error that holds the value, in any form it was written.
func TestStoreErrorsNeverHoldTheValue(t *testing.T) {
	const value = `tok'en\canary-77`
	quoted := macQuote(value)
	mac, _ := macBackend(t, helperCfg{Exit: 1, Stderr: "syntax error in: add-generic-password -w " + quoted + "\n"})
	lin, _ := linuxBackend(t, helperCfg{Exit: 1, Stderr: "could not store " + value + "\n"})
	for name, b := range map[string]*Backend{"mac": mac, "linux": lin} {
		err := b.Store("acme", value)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		for _, form := range []string{value, quoted, "canary-77"} {
			if strings.Contains(err.Error(), form) {
				t.Errorf("%s: the error holds %q: %v", name, form, err)
			}
		}
	}
}

// Review M3: `security find-generic-password -w` prints a value with
// non-ASCII characters as hex, so a round trip would return different text.
// macOS refuses to store such a value instead of guessing on the way back.
func TestMacStoreRefusesNonASCIIValues(t *testing.T) {
	mac, p := macBackend(t, helperCfg{})
	err := mac.Store("acme", "tökén-canary")
	if err == nil || !strings.Contains(err.Error(), "ASCII") || !strings.Contains(err.Error(), "macOS") || strings.Contains(err.Error(), "canary") {
		t.Fatalf("%v", err)
	}
	if recs := calls(t, p); len(recs) != 0 {
		t.Errorf("security ran: %v", recs)
	}
	lin, _ := linuxBackend(t, helperCfg{})
	if err := lin.Store("acme", "tökén"); err != nil {
		t.Errorf("Linux stores UTF-8: %v", err)
	}
}
