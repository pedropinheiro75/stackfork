package redact

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestRedactsRawValue(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, []Secret{{Name: "db_password", Value: "supersecret123"}})
	if _, err := w.Write([]byte("connecting with supersecret123 now\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "supersecret123") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "[redacted:db_password]") {
		t.Fatalf("expected redaction marker, got %q", got)
	}
}

func TestRedactsBase64Form(t *testing.T) {
	var out bytes.Buffer
	value := "supersecret123"
	w := NewWriter(&out, []Secret{{Name: "token", Value: value}})
	b64 := base64.StdEncoding.EncodeToString([]byte(value))
	if _, err := w.Write([]byte("token=" + b64 + "\n")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	got := out.String()
	if strings.Contains(got, b64) {
		t.Fatalf("base64 secret leaked: %q", got)
	}
	if !strings.Contains(got, "[redacted:token]") {
		t.Fatalf("expected redaction marker, got %q", got)
	}
}

// TestValueSplitAcrossWrites is the task 4.1a check: a secret value that
// straddles two Write calls must still be caught.
func TestValueSplitAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	value := "supersecret123"
	w := NewWriter(&out, []Secret{{Name: "npm_token", Value: value}})
	first, second := value[:8], value[8:]
	if _, err := w.Write([]byte("prefix " + first)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(second + " suffix\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, value) {
		t.Fatalf("split secret leaked: %q", got)
	}
	if !strings.Contains(got, "[redacted:npm_token]") {
		t.Fatalf("expected redaction marker, got %q", got)
	}
	if !strings.Contains(got, "prefix ") || !strings.Contains(got, " suffix") {
		t.Fatalf("expected surrounding text preserved, got %q", got)
	}
}

func TestShortValuesNeverMatched(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, []Secret{{Name: "short", Value: "abc123"}}) // 6 chars, under the 8-char floor
	if _, err := w.Write([]byte("value is abc123 here\n")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	got := out.String()
	if !strings.Contains(got, "abc123") {
		t.Fatalf("expected the short value to pass through unredacted, got %q", got)
	}
	if strings.Contains(got, "[redacted:") {
		t.Fatalf("did not expect any redaction marker, got %q", got)
	}
}

func TestManyChunkedWrites(t *testing.T) {
	var out bytes.Buffer
	value := "supersecret123"
	w := NewWriter(&out, []Secret{{Name: "s", Value: value}})
	msg := "aaaa" + value + "bbbb"
	for i := 0; i < len(msg); i++ {
		if _, err := w.Write([]byte{msg[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, value) {
		t.Fatalf("secret leaked across byte-at-a-time writes: %q", got)
	}
	if got != "aaaa[redacted:s]bbbb" {
		t.Fatalf("unexpected output: %q", got)
	}
}
