package dashboard

import "strings"

// Pure builders for the Windows-only dialog and terminal launcher; kept
// OS-neutral so they are tested on every platform.

// psq is a PowerShell single-quoted string: only ' is special (doubled).
func psq(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// psMessageBoxScript shows an OK/Cancel warning box, default Cancel, and
// prints the button pressed ("OK" or "Cancel").
func psMessageBoxScript(title, text string) string {
	return "Add-Type -AssemblyName System.Windows.Forms\n" +
		"[System.Windows.Forms.MessageBox]::Show(" + psq(text) + ", " + psq(title) +
		", [System.Windows.Forms.MessageBoxButtons]::OKCancel" +
		", [System.Windows.Forms.MessageBoxIcon]::Warning" +
		", [System.Windows.Forms.MessageBoxDefaultButton]::Button2)\n"
}

// psLauncherScript is the Windows twin of the macOS .command script: cd into
// the repo, run the agent with the prompt read from a file (never on a
// command line), and leave the shell open (the caller starts it -NoExit).
func psLauncherScript(dir, bin, flag, promptPath string) string {
	run := "& " + psq(bin)
	if flag != "" {
		run += " " + psq(flag)
	}
	return "Set-Location -LiteralPath " + psq(dir) + "\n" +
		"$prompt = Get-Content -LiteralPath " + psq(promptPath) + " -Raw -Encoding UTF8\n" +
		run + " $prompt\n"
}
