package secretstore

import (
	"strings"
	"testing"
)

func linuxBackend(t *testing.T, cfg helperCfg) (*Backend, string) {
	p := newHelper(t, cfg)
	return &Backend{GOOS: "linux", SecretTool: p}, p
}

func TestLinuxLookupAndSearchArgv(t *testing.T) {
	b, p := linuxBackend(t, helperCfg{Stdout: "s3cret-canary"})
	v, st, err := b.Resolve("keychain:acme-npm-token")
	if v != "s3cret-canary" || st != Resolves || err != nil {
		t.Fatalf("%q %v %v", v, st, err)
	}
	// search output holds attributes and may hold the secret: it is discarded.
	if st := b.Exists("keychain:acme-npm-token"); st != Resolves {
		t.Fatal(st)
	}
	c := calls(t, p)
	if len(c) != 2 ||
		strings.Join(c[0].Argv, " ") != "lookup service stackfork account acme-npm-token" ||
		strings.Join(c[1].Argv, " ") != "search service stackfork account acme-npm-token" {
		t.Fatalf("%+v", c)
	}
}

func TestLinuxStatusMapping(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  helperCfg
		want Status
	}{
		"missing":     {helperCfg{Exit: 1}, Missing},
		"empty ok":    {helperCfg{Exit: 0}, Missing},
		"no dbus":     {helperCfg{Exit: 1, Stderr: "secret-tool: Cannot autolaunch D-Bus without X11 $DISPLAY\n"}, Unavailable},
		"no service":  {helperCfg{Exit: 1, Stderr: "secret-tool: The name org.freedesktop.secrets was not provided by any .service files\n"}, Unavailable},
		"no bus conn": {helperCfg{Exit: 1, Stderr: "Could not connect: No such file or directory\n"}, Unavailable},
		"locked":      {helperCfg{Exit: 1, Stderr: "secret-tool: Cannot get secret of a locked object\n"}, Locked},
		"other":       {helperCfg{Exit: 2, Stderr: "weird failure\nmore\n"}, Error},
	} {
		b, _ := linuxBackend(t, tc.cfg)
		v, st, err := b.Resolve("keychain:acme")
		if v != "" || st != tc.want || err == nil {
			t.Errorf("%s: %q %v %v", name, v, st, err)
		}
		if tc.want != Resolves && name != "empty ok" {
			if got := b.Exists("keychain:acme"); got != tc.want {
				t.Errorf("%s: Exists = %v, want %v", name, got, tc.want)
			}
		}
	}
}

func TestLinuxStoreWritesThroughStdin(t *testing.T) {
	const value = "tok-canary-9f3a"
	b, p := linuxBackend(t, helperCfg{})
	if err := b.Store("acme-npm-token", value); err != nil {
		t.Fatal(err)
	}
	c := calls(t, p)
	if len(c) != 1 || c[0].Stdin != value ||
		strings.Join(c[0].Argv, "|") != "store|--label|stackfork acme-npm-token|service|stackfork|account|acme-npm-token" {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(strings.Join(c[0].Argv, " "), value) {
		t.Fatal("value in argv")
	}
}

func TestLinuxStoreFailureMapping(t *testing.T) {
	for name, cfg := range map[string]helperCfg{
		"locked":      {Exit: 1, Stderr: "secret-tool: Cannot create an item in a locked collection\n"},
		"no dbus":     {Exit: 1, Stderr: "Cannot autolaunch D-Bus without X11 $DISPLAY\n"},
		"other error": {Exit: 1, Stderr: "boom\n"},
	} {
		b, _ := linuxBackend(t, cfg)
		err := b.Store("acme", "tok-canary")
		if err == nil || strings.Contains(err.Error(), "tok-canary") {
			t.Errorf("%s: %v", name, err)
		}
		if name == "locked" && !strings.Contains(err.Error(), "locked") {
			t.Errorf("locked: %v", err)
		}
		if name == "no dbus" && !strings.Contains(err.Error(), "Secret Service") {
			t.Errorf("no dbus: %v", err)
		}
	}
}
