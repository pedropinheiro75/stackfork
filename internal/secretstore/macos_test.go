package secretstore

import (
	"strings"
	"testing"
)

// unquoteSecurityLine is the reference parser for the single line written to
// `security -i`: add-generic-password -U -s stackfork -a ITEM -w '<value>'.
func unquoteSecurityLine(t *testing.T, line, item string) string {
	t.Helper()
	prefix := "add-generic-password -U -s stackfork -a " + item + " -w '"
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "'\n") {
		t.Fatalf("unexpected line shape: %q", line)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "'\n")
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\\':
			i++
			if i >= len(body) {
				t.Fatalf("dangling escape in %q", line)
			}
			out.WriteByte(body[i])
		case c == '\'':
			t.Fatalf("unescaped quote in %q", line)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

func macBackend(t *testing.T, cfg helperCfg) (*Backend, string) {
	p := newHelper(t, cfg)
	return &Backend{GOOS: "darwin", Security: p}, p
}

func TestMacReadUsesDashWAndStdout(t *testing.T) {
	b, p := macBackend(t, helperCfg{Stdout: "s3cret-canary\n"})
	v, st, err := b.Resolve("keychain:acme-npm-token")
	if v != "s3cret-canary" || st != Resolves || err != nil {
		t.Fatalf("%q %v %v", v, st, err)
	}
	c := calls(t, p)
	if len(c) != 1 || strings.Join(c[0].Argv, " ") != "find-generic-password -s stackfork -a acme-npm-token -w" {
		t.Fatalf("%+v", c)
	}
}

func TestMacExistsReadsNoValue(t *testing.T) {
	b, p := macBackend(t, helperCfg{Stdout: "attributes only\n"})
	if st := b.Exists("keychain:acme-npm-token"); st != Resolves {
		t.Fatal(st)
	}
	c := calls(t, p)
	if len(c) != 1 || strings.Join(c[0].Argv, " ") != "find-generic-password -s stackfork -a acme-npm-token" {
		t.Fatalf("%+v", c)
	}
}

func TestMacStatusMapping(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  helperCfg
		want Status
	}{
		"44 missing":   {helperCfg{Exit: 44, Stderr: "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.\n"}, Missing},
		"36 locked":    {helperCfg{Exit: 36}, Locked},
		"interaction":  {helperCfg{Exit: 51, Stderr: "User interaction is not allowed.\n"}, Locked},
		"other error":  {helperCfg{Exit: 1, Stderr: "\x1b[1mboom\x1b[0m\nsecond line\n"}, Error},
		"empty output": {helperCfg{Exit: 0, Stdout: "\n"}, Missing},
		"timeout":      {helperCfg{SleepMS: 3000}, Error},
	} {
		b, _ := macBackend(t, tc.cfg)
		b.Timeout = 300 * 1000 * 1000
		v, st, err := b.Resolve("keychain:acme")
		if v != "" || st != tc.want || err == nil {
			t.Errorf("%s: %q %v %v", name, v, st, err)
		}
		if tc.want == Error && name == "other error" && (strings.Contains(err.Error(), "second line") || strings.ContainsRune(err.Error(), 0x1b)) {
			t.Errorf("error is not the cleaned first line: %q", err)
		}
	}
}

func TestMacMissingToolIsUnavailable(t *testing.T) {
	b := &Backend{GOOS: "darwin", Security: "/nonexistent/security"}
	if _, st, err := b.Resolve("keychain:acme"); st != Unavailable || err == nil {
		t.Fatalf("%v %v", st, err)
	}
	if err := b.Store("acme", "v"); err == nil {
		t.Fatal("stored without a tool")
	}
}

func TestMacStoreWritesThroughStdinOnly(t *testing.T) {
	const value = "tok-canary-9f3a"
	b, p := macBackend(t, helperCfg{})
	if err := b.Store("acme-npm-token", value); err != nil {
		t.Fatal(err)
	}
	c := calls(t, p)
	if len(c) != 2 { // the write, then the existence check
		t.Fatalf("%d calls", len(c))
	}
	for _, rec := range c {
		if strings.Contains(strings.Join(rec.Argv, " "), value) {
			t.Fatal("value in argv")
		}
	}
	if strings.Join(c[0].Argv, " ") != "-i" {
		t.Fatalf("argv %v", c[0].Argv)
	}
	if got := unquoteSecurityLine(t, c[0].Stdin, "acme-npm-token"); got != value {
		t.Fatalf("round trip %q", got)
	}
	if strings.Join(c[1].Argv, " ") != "find-generic-password -s stackfork -a acme-npm-token" {
		t.Fatalf("check argv %v", c[1].Argv)
	}
}

func TestMacStoreEveryPrintableASCII(t *testing.T) {
	b, p := macBackend(t, helperCfg{})
	n := 0
	for r := rune(0x20); r <= 0x7e; r++ {
		value := "a" + string(r) + "b" + string(r)
		if err := b.Store("acme", value); err != nil {
			t.Fatalf("%q: %v", r, err)
		}
		n++
	}
	c := calls(t, p)
	i := 0
	for r := rune(0x20); r <= 0x7e; r++ {
		want := "a" + string(r) + "b" + string(r)
		rec := c[i]
		i += 2
		if got := unquoteSecurityLine(t, rec.Stdin, "acme"); got != want {
			t.Errorf("%q: round trip %q, want %q", r, got, want)
		}
		if strings.Count(rec.Stdin, "\n") != 1 {
			t.Errorf("%q: stdin is not one line", r)
		}
	}
}

func TestMacStoreFailureMapping(t *testing.T) {
	b, _ := macBackend(t, helperCfg{Exit: 36, Stderr: "User interaction is not allowed.\n"})
	err := b.Store("acme", "tok-canary")
	if err == nil || strings.Contains(err.Error(), "tok-canary") || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("%v", err)
	}
	// a failing write is a failure
	b2, _ := macBackend(t, helperCfg{Exit: 44})
	if err := b2.Store("acme", "v"); err == nil {
		t.Fatal("silent failure accepted")
	}
}
