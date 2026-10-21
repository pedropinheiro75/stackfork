package secretstore

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Windows tool handling (design D2, slice 2). Pure helpers so every OS
// tests them; wintool_windows.go supplies the owner lookup.

// opPathsWindows lists the fixed places of op.exe. A variable that is unset
// or not an absolute drive path gives no candidate.
func opPathsWindows(getenv func(string) string) []string {
	var out []string
	if pf := absWin(getenv("ProgramFiles")); pf != "" {
		out = append(out, pf+`\1Password CLI\op.exe`)
	}
	if la := absWin(getenv("LOCALAPPDATA")); la != "" {
		out = append(out, la+`\Microsoft\WinGet\Links\op.exe`, la+`\Programs\1Password CLI\op.exe`)
	}
	return out
}

func absWin(p string) string {
	p = strings.TrimRight(p, `\`)
	if len(p) < 3 || p[1] != ':' || p[2] != '\\' || !unicode.IsLetter(rune(p[0])) {
		return ""
	}
	return p
}

// winToolEnv is the minimal environment of a store tool on Windows: the
// variables op.exe and PowerShell need to find the user profile, a fixed
// PATH, and for op the OP_* variables. Names match case-insensitively.
func winToolEnv(environ []string, forOP bool) []string {
	keep := map[string]bool{"SYSTEMROOT": true, "WINDIR": true, "USERPROFILE": true, "APPDATA": true,
		"LOCALAPPDATA": true, "TEMP": true, "TMP": true, "USERNAME": true, "PROGRAMDATA": true}
	root := `C:\Windows`
	var rest []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		u := strings.ToUpper(k)
		if u == "SYSTEMROOT" && v != "" {
			root = v
		}
		if keep[u] || (forOP && strings.HasPrefix(u, "OP_")) {
			rest = append(rest, kv)
		}
	}
	return append([]string{`PATH=` + root + `\System32`}, rest...)
}

// toolModeOK is the file-type part of checkTool. Windows reports no
// executable or group bits, so it asks for a regular .exe file; the owner
// check (ownerOK) and the scanned-root check still run.
func toolModeOK(goos string, mode fs.FileMode, name string) error {
	if goos == "windows" {
		if !mode.IsRegular() || !strings.EqualFold(filepath.Ext(strings.ReplaceAll(name, `\`, "/")), ".exe") {
			return errors.New("not a regular .exe file")
		}
		return nil
	}
	if !mode.IsRegular() || mode.Perm()&0o111 == 0 {
		return errors.New("not an executable file")
	}
	if mode.Perm()&0o022 != 0 {
		return errors.New("the file is writable by group or others")
	}
	return nil
}

const trustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"

// ownerSIDAllowed: the owner of a store tool and its directory is the
// current user, SYSTEM, Administrators or TrustedInstaller.
func ownerSIDAllowed(owner, current string) bool {
	switch owner {
	case "":
		return false
	case "S-1-5-18", "S-1-5-32-544", trustedInstallerSID:
		return true
	}
	return owner == current
}

// consoleModeNoEcho clears ENABLE_ECHO_INPUT.
func consoleModeNoEcho(mode uint32) uint32 { return mode &^ 0x4 }

// psQuote makes a PowerShell single-quoted literal. Both ASCII and curly
// single quotes end a literal in PowerShell, so every one is doubled;
// control characters become spaces.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch {
		case unicode.IsControl(r):
			b.WriteByte(' ')
		case r == '\'' || r == '\u2018' || r == '\u2019' || r == '\u201A' || r == '\u201B':
			b.WriteRune(r)
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// powershellDialog builds the command of the hidden-value dialog (D14,
// Windows): Get-Credential masks the typed value and the script writes it
// to stdout. The script travels as -EncodedCommand, so no quoting of the
// names reaches the command line parser; the value is never in argv.
func powershellDialog(systemRoot, repo, name, item string) (string, []string) {
	msg := "Set " + name + " for " + repo + " as keychain:" + item + ". Type the value in the password box."
	script := "$c = Get-Credential -UserName " + psQuote("keychain:"+item) + " -Message " + psQuote(msg) + "\n" +
		"if ($null -eq $c) { exit 1 }\n" +
		"[Console]::Out.Write($c.GetNetworkCredential().Password)\n"
	return systemRoot + `\System32\WindowsPowerShell\v1.0\powershell.exe`,
		[]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", base64.StdEncoding.EncodeToString(utf16le(script))}
}

// HiddenValueDialog shows the PowerShell password dialog and returns what
// the person typed (Windows only). The value comes back on stdout, never in
// argv; an empty answer or a cancelled dialog is an error.
func (b *Backend) HiddenValueDialog(repo, name, item string) (string, error) {
	if b.goos() != "windows" {
		return "", errors.New("the PowerShell dialog is Windows only")
	}
	root := os.Getenv("SystemRoot")
	if absWin(root) == "" {
		root = `C:\Windows`
	}
	path, args := powershellDialog(root, repo, name, item)
	tool, err := resolveTool([]string{path}, b.roots())
	if err != nil {
		return "", err
	}
	r := runTool(tool, args, "", winToolEnv(os.Environ(), false), 5*time.Minute)
	if r.err != nil || r.exit != 0 {
		return "", errors.New("the dialog was cancelled or failed")
	}
	v := strings.TrimRight(r.stdout, "\r\n")
	if err := ValidateValue(v); err != nil {
		return "", err
	}
	return v, nil
}
