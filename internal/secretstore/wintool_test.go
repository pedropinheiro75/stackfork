package secretstore

import (
	"encoding/base64"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

func TestOpPathsWindows(t *testing.T) {
	env := map[string]string{"ProgramFiles": `C:\Program Files`, "LOCALAPPDATA": `C:\Users\ana\AppData\Local`}
	got := opPathsWindows(func(k string) string { return env[k] })
	want := []string{
		`C:\Program Files\1Password CLI\op.exe`,
		`C:\Users\ana\AppData\Local\Microsoft\WinGet\Links\op.exe`,
		`C:\Users\ana\AppData\Local\Programs\1Password CLI\op.exe`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	// unset or relative variables yield no candidate: nothing is resolved from the current directory
	env["ProgramFiles"], env["LOCALAPPDATA"] = "", `AppData\Local`
	if got := opPathsWindows(func(k string) string { return env[k] }); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}

func TestWinToolEnv(t *testing.T) {
	environ := []string{
		`Path=C:\evil`, `SystemRoot=C:\Windows`, `LocalAppData=C:\Users\ana\AppData\Local`,
		`USERPROFILE=C:\Users\ana`, `OP_SERVICE_ACCOUNT_TOKEN=tok`, `NPM_TOKEN=leak`, `OPX=no`,
	}
	has := func(env []string, kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	plain := winToolEnv(environ, false)
	if !has(plain, `PATH=C:\Windows\System32`) || !has(plain, `SystemRoot=C:\Windows`) || !has(plain, `USERPROFILE=C:\Users\ana`) {
		t.Fatalf("%q", plain)
	}
	for _, bad := range []string{`Path=C:\evil`, "NPM_TOKEN=leak", "OP_SERVICE_ACCOUNT_TOKEN=tok", "OPX=no"} {
		if has(plain, bad) {
			t.Errorf("leaked %q", bad)
		}
	}
	if op := winToolEnv(environ, true); !has(op, "OP_SERVICE_ACCOUNT_TOKEN=tok") || has(op, "OPX=no") {
		t.Fatalf("%q", op)
	}
	if e := winToolEnv(nil, false); !has(e, `PATH=C:\Windows\System32`) {
		t.Fatalf("default SystemRoot: %q", e)
	}
}

func TestToolModeOK(t *testing.T) {
	var perm fs.FileMode = 0o666 // what Go reports for a Windows file
	if err := toolModeOK("windows", perm, `C:\x\op.exe`); err != nil {
		t.Fatal(err)
	}
	if err := toolModeOK("windows", perm, `C:\x\OP.EXE`); err != nil {
		t.Fatal(err)
	}
	if err := toolModeOK("windows", perm, `C:\x\op.cmd`); err == nil {
		t.Fatal("op.cmd accepted")
	}
	if err := toolModeOK("windows", fs.ModeSymlink|perm, `C:\x\op.exe`); err == nil {
		t.Fatal("non-regular accepted")
	}
	if err := toolModeOK("linux", 0o755, "/usr/bin/op"); err != nil {
		t.Fatal(err)
	}
	if err := toolModeOK("linux", 0o666, "/usr/bin/op"); err == nil {
		t.Fatal("unix: no exec bit and group-writable accepted")
	}
}

func TestOwnerSIDAllowed(t *testing.T) {
	me := "S-1-5-21-1-2-3-1001"
	for sid, want := range map[string]bool{
		me:                    true,
		"S-1-5-18":            true, // SYSTEM
		"S-1-5-32-544":        true, // Administrators
		"S-1-5-21-1-2-3-1002": false,
		"S-1-1-0":             false, // Everyone
		"":                    false,
	} {
		if got := ownerSIDAllowed(sid, me); got != want {
			t.Errorf("%q: %v", sid, got)
		}
	}
	if ownerSIDAllowed("", "") {
		t.Fatal("empty matches empty")
	}
	if !ownerSIDAllowed(trustedInstallerSID, me) {
		t.Fatal("TrustedInstaller")
	}
}

func TestConsoleModeNoEcho(t *testing.T) {
	const echo, line, other = 0x4, 0x2, 0x1
	if got := consoleModeNoEcho(echo | line | other); got != line|other {
		t.Fatalf("%#x", got)
	}
}

func TestPowerShellDialog(t *testing.T) {
	path, args := powershellDialog(`C:\Windows`, "acme-web", "NPM_TOKEN", "acme-npm-token")
	if path != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatal(path)
	}
	if len(args) != 6 || args[0] != "-NoProfile" || args[1] != "-NonInteractive" || args[2] != "-ExecutionPolicy" || args[3] != "Bypass" || args[4] != "-EncodedCommand" {
		t.Fatalf("%q", args)
	}
	raw, err := base64.StdEncoding.DecodeString(args[5])
	if err != nil {
		t.Fatal(err)
	}
	script, _ := decodeBlob(raw)
	for _, want := range []string{"Get-Credential", "keychain:acme-npm-token", "acme-web", "NPM_TOKEN", "GetNetworkCredential().Password"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q: %s", want, script)
		}
	}
}

func TestPSQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      "'plain'",
		"it's":       "'it''s'",
		"a\u2018b":   "'a\u2018\u2018b'",
		"a\u201Bb":   "'a\u201B\u201Bb'",
		"x\n$(calc)": "'x $(calc)'", // control characters become spaces; single quotes keep $() literal
	} {
		if got := psQuote(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
