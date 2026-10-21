package secretstore

import (
	"strings"
	"testing"
)

func TestStatusValues(t *testing.T) {
	for s, want := range map[Status]string{
		Resolves: "resolves", Missing: "missing", Locked: "locked",
		Unavailable: "unavailable", Error: "error", NotChecked: "not checked",
	} {
		if string(s) != want {
			t.Errorf("status %q, want %q", s, want)
		}
	}
}

func TestParseRef(t *testing.T) {
	k, item, err := ParseRef("keychain:acme-npm-token")
	if err != nil || k != KindKeychain || item != "acme-npm-token" {
		t.Fatalf("%v %q %v", k, item, err)
	}
	k, item, err = ParseRef("op://Dev/acme-stripe/test-key")
	if err != nil || k != KindOP || item != "op://Dev/acme-stripe/test-key" {
		t.Fatalf("%v %q %v", k, item, err)
	}
	if _, _, err := ParseRef("env:X"); err == nil {
		t.Fatal("accepted env:")
	}
}

func TestInvalidRefIsAnErrorStatusWithoutEcho(t *testing.T) {
	b := &Backend{}
	for _, ref := range []string{"keychain:bad item canaryValue", "op://a/b?canaryValue", "env:X"} {
		v, st, err := b.Resolve(ref)
		if v != "" || st != Error || err == nil || strings.Contains(err.Error(), "canaryValue") {
			t.Errorf("%q: %q %v %v", ref, v, st, err)
		}
		if st := b.Exists(ref); st != Error {
			t.Errorf("Exists(%q) = %v", ref, st)
		}
	}
}

func TestStoreRefusesBadItemAndValue(t *testing.T) {
	b := &Backend{GOOS: "linux"}
	big := strings.Repeat("x", MaxValueSize+1)
	for name, c := range map[string]struct{ item, value string }{
		"bad item":  {"bad item", "v"},
		"empty":     {"acme", ""},
		"newline":   {"acme", "a\nb"},
		"carriage":  {"acme", "a\rb"},
		"nul":       {"acme", "a\x00b"},
		"too large": {"acme", big},
	} {
		err := b.Store(c.item, c.value)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "xxxx") {
			t.Errorf("%s: error echoes the value", name)
		}
	}
	if err := ValidateValue(strings.Repeat("x", MaxValueSize)); err != nil {
		t.Errorf("64 KiB exactly must pass: %v", err)
	}
}
