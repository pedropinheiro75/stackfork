package dashboard

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPSQuoteDoublesEverySingleQuoteLookalike(t *testing.T) {
	for _, q := range []string{"'", "\u2018", "\u2019", "\u201a", "\u201b"} {
		if got, want := PSQuote("a"+q+"b"), "'a"+q+q+"b'"; got != want {
			t.Errorf("PSQuote(%q) = %q, want %q", q, got, want)
		}
	}
	if got := PSQuote("$x `y\n\""); got != "'$x `y\n\"'" {
		t.Errorf("other characters stay literal: %q", got)
	}
}

func TestPSEncodedCommandIsUTF16LEBase64(t *testing.T) {
	const script = "Write-Output 'h\u00e9llo \u2019'"
	raw, err := base64.StdEncoding.DecodeString(psEncodedCommand(script))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("not base64 of UTF-16: %v", err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(u)); got != script {
		t.Fatalf("round trip = %q", got)
	}
}

func TestPSMessageBoxScriptIsConstant(t *testing.T) {
	s := psMessageBoxScript()
	for _, want := range []string{"$env:SF_TITLE", "$env:SF_TEXT", "OKCancel", "Warning", "Button2", "DefaultDesktopOnly"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	env := confirmEnv([]string{"A=1"}, "t\u2019 \u2018 \u201a \u201b ' \" ` $", "x\n$(calc)")
	if len(env) != 3 || env[0] != "A=1" || !strings.HasPrefix(env[1], "SF_TITLE=t\u2019") || env[2] != "SF_TEXT=x\n$(calc)" {
		t.Fatalf("env = %q", env)
	}
}

func TestPSLauncherScript(t *testing.T) {
	s := psLauncherScript(`C:\Users\o'neil\re`+"\u2019"+`po`, "copilot", "-i", `C:\s\learn-1.prompt.txt`)
	for _, want := range []string{
		`Set-Location -LiteralPath 'C:\Users\o''neil\re` + "\u2019\u2019" + `po'`,
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

func TestPSLauncherFileHasBOM(t *testing.T) {
	b := psLauncherFile("Jo\u00e3o")
	if !strings.HasPrefix(string(b), "\ufeff") || !strings.Contains(string(b), "Jo\u00e3o") {
		t.Fatalf("%q", b)
	}
}

func TestWTUsable(t *testing.T) {
	if !wtUsable(`C:\a b\repo`) || wtUsable(`C:\a;b\repo`) {
		t.Fatal("a ';' in the directory must refuse wt.exe")
	}
}
