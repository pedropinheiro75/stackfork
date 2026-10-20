package lifecycle

import (
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestStepBlockQuotesControlCharacters(t *testing.T) {
	s := knowledge.Step{ID: "app", Kind: knowledge.KindHost, Cmd: "npm run dev\n    cmd: echo ok", Name: "web"}
	b := strings.Join(StepBlock(s), "\n")
	if strings.Count(b, "\n") != 2 || !strings.Contains(b, `"npm run dev\n    cmd: echo ok"`) {
		t.Fatalf("control characters must be quoted:\n%s", b)
	}
	if got := StepBlock(knowledge.Step{ID: "a\nb", Kind: "host"})[0]; strings.Contains(got, "\n") {
		t.Fatalf("id not quoted: %q", got)
	}
}

func TestTrustPromptIsCapped(t *testing.T) {
	var p knowledge.Plan
	for i := 0; i < 200; i++ {
		p.Steps = append(p.Steps, knowledge.Step{ID: "s" + strings.Repeat("x", 5) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Kind: knowledge.KindHost, Cmd: "npm run dev"})
	}
	out, err := TrustPrompt(p, nil)
	if err != ErrTrustPromptTooLong || out != "" || !strings.Contains(err.Error(), "`stackfork plan --trust`") {
		t.Fatalf("an over-long prompt must be refused: %q %v", out, err)
	}
	short, err := TrustPrompt(knowledge.Plan{Steps: p.Steps[:3]}, nil)
	if err != nil || !strings.Contains(short, "npm run dev") {
		t.Fatalf("%q %v", short, err)
	}
}

func TestReadableDiffReorderedAndTrusted(t *testing.T) {
	a := knowledge.Step{ID: "a", Kind: knowledge.KindHost, Cmd: "x"}
	b := knowledge.Step{ID: "b", Kind: knowledge.KindHost, Cmd: "y"}
	base, main := t.TempDir(), t.TempDir()
	p := knowledge.Plan{Steps: []knowledge.Step{a, b}}
	if _, err := TrustHere(base, main, p); err != nil {
		t.Fatal(err)
	}
	if d := ReadableDiff(base, main, p); len(d) != 0 {
		t.Fatalf("trusted plan has no diff: %v", d)
	}
	d := ReadableDiff(base, main, knowledge.Plan{Steps: []knowledge.Step{b, a}})
	if len(d) != 1 || d[0] != "steps were reordered" {
		t.Fatalf("%v", d)
	}
}
