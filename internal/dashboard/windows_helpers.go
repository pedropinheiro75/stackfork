package dashboard

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

// Pure builders for the Windows-only dialog and terminal launcher; kept
// OS-neutral so they are tested on every platform.

// PSQuote is a PowerShell single-quoted string. PowerShell treats U+2018,
// U+2019, U+201A and U+201B as single quotes too, so all five are doubled.
func PSQuote(s string) string {
	return "'" + strings.NewReplacer("'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019",
		"\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b").Replace(s) + "'"
}

// psEncodedCommand is script as UTF-16LE base64, the form -EncodedCommand
// takes, so the script needs no command-line quoting.
func psEncodedCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, 2*len(u))
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// psMessageBoxScript shows an OK/Cancel warning box, default Cancel, in front,
// and prints the button pressed ("OK" or "Cancel"). It is constant: the
// title and text arrive in the environment (confirmEnv), never in the script.
func psMessageBoxScript() string {
	return "Add-Type -AssemblyName System.Windows.Forms\n" +
		"[System.Windows.Forms.MessageBox]::Show($env:SF_TEXT, $env:SF_TITLE" +
		", [System.Windows.Forms.MessageBoxButtons]::OKCancel" +
		", [System.Windows.Forms.MessageBoxIcon]::Warning" +
		", [System.Windows.Forms.MessageBoxDefaultButton]::Button2" +
		", [System.Windows.Forms.MessageBoxOptions]::DefaultDesktopOnly)\n"
}

// confirmEnv is base plus the values psMessageBoxScript reads.
func confirmEnv(base []string, title, text string) []string {
	return append(base[:len(base):len(base)], "SF_TITLE="+title, "SF_TEXT="+text)
}

// wtUsable: wt.exe splits its command line on ';', so a directory holding one
// would start a second command.
func wtUsable(path string) bool { return !strings.Contains(path, ";") }

// psLauncherFile is the .ps1 bytes: a UTF-8 BOM so Windows PowerShell 5.1
// reads non-ASCII paths correctly.
func psLauncherFile(script string) []byte { return []byte("\ufeff" + script) }

// psLauncherScript is the Windows twin of the macOS .command script: cd into
// the repo, run the agent with the prompt read from a file (never on a
// command line), and leave the shell open (the caller starts it -NoExit).
func psLauncherScript(dir, bin, flag, promptPath string) string {
	run := "& " + PSQuote(bin)
	if flag != "" {
		run += " " + PSQuote(flag)
	}
	return "Set-Location -LiteralPath " + PSQuote(dir) + "\n" +
		"$prompt = Get-Content -LiteralPath " + PSQuote(promptPath) + " -Raw -Encoding UTF8\n" +
		run + " $prompt\n"
}
