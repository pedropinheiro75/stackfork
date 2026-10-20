package dashboard

import (
	"strings"
	"testing"
)

func TestPSMessageBoxScriptQuotes(t *testing.T) {
	s := psMessageBoxScript("it's", "don't\nrun")
	for _, want := range []string{"'it''s'", "'don''t\nrun'", "OKCancel", "Warning", "Button2"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
}

func TestPSLauncherScript(t *testing.T) {
	s := psLauncherScript(`C:\Users\o'neil\repo`, "copilot", "-i", `C:\s\learn-1.prompt.txt`)
	for _, want := range []string{
		`Set-Location -LiteralPath 'C:\Users\o''neil\repo'`,
		`Get-Content -LiteralPath 'C:\s\learn-1.prompt.txt' -Raw -Encoding UTF8`,
		`& 'copilot' '-i' $prompt`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("launcher lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(psLauncherScript("d", "claude", "", "p"), "''") {
		t.Error("an empty flag must not become an empty argument")
	}
}
