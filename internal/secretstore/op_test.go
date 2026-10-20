package secretstore

import (
	"strings"
	"testing"
	"time"
)

const opRef = "op://Dev/acme-stripe/test-key"

func opBackend(t *testing.T, cfg helperCfg) (*Backend, string) {
	p := newHelper(t, cfg)
	return &Backend{GOOS: "darwin", OpPaths: []string{p}}, p
}

func TestOPReadArgvAndExactValue(t *testing.T) {
	b, p := opBackend(t, helperCfg{Stdout: "sk_test_canary\n"})
	v, st, err := b.Resolve(opRef)
	// --no-newline means the output is the value, byte for byte.
	if v != "sk_test_canary\n" || st != Resolves || err != nil {
		t.Fatalf("%q %v %v", v, st, err)
	}
	c := calls(t, p)
	if len(c) != 1 || strings.Join(c[0].Argv, "|") != "read|--no-newline|"+opRef {
		t.Fatalf("%+v", c)
	}
}

func TestOPEnvironmentIsMinimalPlusOPVariables(t *testing.T) {
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "svc-canary")
	t.Setenv("OP_ACCOUNT", "acme.1password.com")
	t.Setenv("NPM_TOKEN", "npm-canary")
	t.Setenv("STRIPE_KEY", "stripe-canary")
	t.Setenv("PATH", "/evil/bin:/usr/bin")
	b, p := opBackend(t, helperCfg{Stdout: "v"})
	if _, st, err := b.Resolve(opRef); st != Resolves {
		t.Fatalf("%v %v", st, err)
	}
	env := strings.Join(calls(t, p)[0].Env, "\n")
	for _, want := range []string{"OP_SERVICE_ACCOUNT_TOKEN=svc-canary", "OP_ACCOUNT=acme.1password.com", "PATH=/usr/bin:/bin"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	for _, bad := range []string{"npm-canary", "stripe-canary", "/evil/bin"} {
		if strings.Contains(env, bad) {
			t.Errorf("env leaks %s", bad)
		}
	}
}

func TestOPStatusMapping(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  helperCfg
		want Status
	}{
		"not an item":    {helperCfg{Exit: 1, Stderr: "[ERROR] 2026/01/02 \"acme-stripe\" isn't an item in the \"Dev\" vault.\n"}, Missing},
		"could not find": {helperCfg{Exit: 1, Stderr: "[ERROR] could not find field test-key\n"}, Missing},
		"not a vault":    {helperCfg{Exit: 1, Stderr: "[ERROR] \"Dev\" isn't a vault in this account.\n"}, Missing},
		"not signed in":  {helperCfg{Exit: 1, Stderr: "[ERROR] You are not signed in.\n"}, Locked},
		"authorization":  {helperCfg{Exit: 1, Stderr: "[ERROR] authorization prompt dismissed\n"}, Locked},
		"locked":         {helperCfg{Exit: 1, Stderr: "[ERROR] 1Password is locked\n"}, Locked},
		"other":          {helperCfg{Exit: 1, Stderr: "[ERROR] network down\nmore\n"}, Error},
		"timeout":        {helperCfg{SleepMS: 3000}, Error},
	} {
		b, _ := opBackend(t, tc.cfg)
		b.Timeout = 300 * time.Millisecond
		v, st, err := b.Resolve(opRef)
		if v != "" || st != tc.want || err == nil {
			t.Errorf("%s: %q %v %v", name, v, st, err)
		}
		if name == "other" && strings.Contains(err.Error(), "more") {
			t.Errorf("error is more than the first line: %v", err)
		}
	}
}

func TestOPTimeoutIs30Seconds(t *testing.T) {
	if opTimeout != 30*time.Second || shortTimeout != 10*time.Second {
		t.Fatal(opTimeout, shortTimeout)
	}
	if (&Backend{}).timeout(opTimeout) != 30*time.Second {
		t.Fatal("default op timeout")
	}
}

func TestOPExistsDoesNotRunTheTool(t *testing.T) {
	b, p := opBackend(t, helperCfg{})
	if st := b.Exists(opRef); st != NotChecked {
		t.Fatal(st)
	}
	if len(calls(t, p)) != 0 {
		t.Fatal("Exists ran op")
	}
}

func TestOPUnsafeBinaryIsUnavailableAndNeverRuns(t *testing.T) {
	b, p := opBackend(t, helperCfg{Stdout: "v"})
	chmodWorldWritable(t, p)
	_, st, err := b.Resolve(opRef)
	if st != Unavailable || err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("%v %v", st, err)
	}
	if len(calls(t, p)) != 0 {
		t.Fatal("ran an unsafe binary")
	}
}

func TestOPRefNeedsThreeSegments(t *testing.T) {
	// Store only ever targets keychain items (1Password writes are a non-goal).
	if _, _, err := (&Backend{}).Resolve("op://a/b"); err == nil {
		t.Fatal("two-segment op ref accepted")
	}
}
