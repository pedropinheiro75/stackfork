package lifecycle

import (
	"bytes"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestRunSeedNamesServicesWhenItsServiceIsNotInTheStack(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"a":{"build":"."},"b":{}}}`), 0o600)
	var out, errb bytes.Buffer
	err := RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "echo hi", "svc", knowledge.ModeContainer, knowledge.Plan{}, nil, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"seed cannot run: its service svc is not in this stack (this stack has: a, b)", "stackfork learn record step --id seed --kind exec --once data --service SERVICE --cmd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if !strings.Contains(errb.String(), "seed cannot run") {
		t.Errorf("stderr: %s", errb.String())
	}
}

func TestRunSeedSuggestsTheHostWhenNoServiceRunsTheApp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"db":{"image":"postgres:17"}}}`), 0o600)
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "bun run db:seed", Service: "web"}}}
	var out, errb bytes.Buffer
	err := RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "bun run db:seed", "web", knowledge.ModeContainer, plan, nil, nil)
	want := `stackfork learn record step --id seed --kind exec --on host --cmd "bun run db:seed" --once data`
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(errb.String(), want) {
		t.Fatalf("%v\n%s", err, errb.String())
	}
	// no service resolves at all
	errb.Reset()
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"db":{"image":"postgres:17"},"cache":{"image":"redis:7"}}}`), 0o600)
	err = RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "bun run db:seed", "", knowledge.ModeContainer, plan, nil, nil)
	if err == nil || !strings.Contains(errb.String(), "--on host") {
		t.Fatalf("%v\n%s", err, errb.String())
	}
}

func proofStep(t *testing.T, dir, id string) *runs.Step {
	t.Helper()
	rec, err := runs.Begin(dir, runs.Meta{Command: "seed", Mode: knowledge.ModeContainer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Finish(nil) })
	return rec.Step(id, knowledge.KindExec, "seed")
}

func TestRunSeedGivesNoProofToACommandTheCapturedPlanDoesNotHold(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"app":{"build":"."}}}`), 0o600)
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "seed-original", Service: "app"}}}
	step := proofStep(t, dir, "seed")
	var out, errb bytes.Buffer
	RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "seed-other", "app", knowledge.ModeContainer, plan, step, nil)
	if step.DefinitionHash != "" {
		t.Fatal("proof assigned to a command the captured plan does not record")
	}
}

func TestHostSeedRetryRecordsExecutionEvidence(t *testing.T) {
	dir, base := t.TempDir(), t.TempDir()
	seed := knowledge.Step{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnHost, Once: knowledge.OnceData, Cmd: "host-seed"}
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{seed}}
	if err := knowledge.WriteTrust(knowledge.TrustPath(base, dir), knowledge.PlanHash(plan.Steps), plan.Steps); err != nil {
		t.Fatal(err)
	}
	old := HostExec
	HostExec = func(io.Writer, string, string, string, string, string, knowledge.Step) (int, error) { return 0, nil }
	t.Cleanup(func() { HostExec = old })
	step := proofStep(t, dir, "seed")
	var out, errb bytes.Buffer
	if err := RunSeed(&out, &errb, dir, dir, "br", "proj", dir, base, t.TempDir(), "host-seed", "", knowledge.ModeContainer, plan, step, nil); err != nil {
		t.Fatal(err)
	}
	if want := TaskDefinitionHash(dir, seed, plan); step.ExecutedHash != want {
		t.Fatalf("executed hash %q, want %q", step.ExecutedHash, want)
	}
}

func TestPrepareRefreshesTheResolvedServiceInTaskHashes(t *testing.T) {
	dir := t.TempDir()
	install := knowledge.Step{ID: "install", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Cmd: "install", Once: knowledge.OnceDeps}
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{install}}
	rec, err := runs.Begin(dir, runs.Meta{Command: "start-env", Mode: knowledge.ModeContainer})
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Finish(nil)
	r := PlanRunner{Run: rec, InstanceDir: dir, Plan: plan, Mode: knowledge.ModeContainer}
	r.declare(plan.Steps)
	before := rec.Step("install", knowledge.KindExec, "").DefinitionHash
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"app":{"build":"."}}}`), 0o600) // what prepare writes
	r.refreshDefinitions(plan.Steps)
	if got := rec.Step("install", knowledge.KindExec, "").DefinitionHash; got == before || got != TaskDefinitionHash(dir, install, plan) {
		t.Fatalf("hash not refreshed after prepare: %q", got)
	}
}

// The receipt shows that the recorded commands themselves ran to their end: one
// shell, so export, cd and set -e keep their meaning; a prefix that exits
// first, a comment or open heredoc that would swallow the seed, or an
// entrypoint that ignores or only echoes the command leaves none.
func TestTaskReceiptOnlyAfterTheCommandRanToItsEnd(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run the receipt script")
	}
	run := func(entry []string, cmds ...string) (bool, error, string) {
		r := newTaskReceipt()
		var out strings.Builder
		argv := append(entry, r.command(cmds...)...)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = r.wrap(&out, &out)
		err := cmd.Run()
		return r.Seen(), err, out.String()
	}
	for _, tc := range []struct {
		name          string
		entry, cmds   []string
		receipt, okay bool
	}{
		{"plain", nil, []string{"true"}, true, true},
		{"failing", nil, []string{"false"}, false, false},
		{"exit 0 prefix", nil, []string{"exit 0 && echo seed"}, false, true},
		{"set -e", nil, []string{"set -e; false; true"}, false, false},
		{"exit 0 migrate", nil, []string{"exit 0", "echo seed"}, false, true},
		{"failing migrate", nil, []string{"false", "echo seed"}, false, false},
		{"state kept across parts", nil, []string{"export X=1; cd /; echo x > /tmp/sf-receipt-test", "[ \"$X\" = 1 ] && [ \"$(pwd)\" = / ]"}, true, true},
		{"comment in migrate", nil, []string{"echo migrated # comment", "false"}, false, false},
		{"open heredoc in migrate", nil, []string{"cat <<EOF\nx", "false"}, false, false},
		{"own arguments hidden", nil, []string{"[ -z \"$1$2$3\" ]"}, true, true},
		{"entrypoint ignores", []string{"sh", "-c", "exit 0"}, []string{"true"}, false, true},
		{"entrypoint echoes $*", []string{"sh", "-c", "echo \"$*\"; exit 0"}, []string{"true"}, false, true},
		{"entrypoint echoes each arg", []string{"sh", "-c", "for a; do echo \"$a\"; done; exit 0", "sh"}, []string{"true"}, false, true},
	} {
		seen, err, out := run(tc.entry, tc.cmds...)
		if seen != tc.receipt || (err == nil) != tc.okay {
			t.Errorf("%s: receipt %t, exit ok %t; want %t, %t (output %q)", tc.name, seen, err == nil, tc.receipt, tc.okay, out)
		}
	}

	// a shell that inherits nounset still runs a single command
	for _, cmds := range [][]string{{"true"}, {"true", "true"}} {
		r := newTaskReceipt()
		argv := r.command(cmds...)
		cmd := exec.Command(argv[0], append([]string{"-u"}, argv[1:]...)...)
		cmd.Stdout, cmd.Stderr = r.wrap(io.Discard, io.Discard)
		if err := cmd.Run(); err != nil || !r.Seen() {
			t.Fatalf("sh -u with %d command(s): err %v, receipt %t", len(cmds), err, r.Seen())
		}
	}

	// split across writes, and only as a whole line
	r := newTaskReceipt()
	stdout, _ := r.wrap(io.Discard, io.Discard)
	for _, c := range append(append([]byte("noise\n"), r.want...), '\n') {
		stdout.Write([]byte{c})
	}
	if !r.Seen() {
		t.Fatal("receipt split across writes was missed")
	}
	r = newTaskReceipt()
	stdout, stderr := r.wrap(io.Discard, io.Discard)
	stdout.Write(append(append([]byte("prefix "), r.want...), '\n'))
	stderr.Write(append(append([]byte(nil), r.want...), '\n'))
	if r.Seen() {
		t.Fatal("receipt inside a longer line, or on stderr, counted")
	}
}

// Both streams of a task reach one step log through the receipt: they write
// under one lock (go test -race).
func TestTaskReceiptSerializesBothStreams(t *testing.T) {
	var log bytes.Buffer // not safe for concurrent use
	r := newTaskReceipt()
	stdout, stderr := r.wrap(&log, &log)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			stderr.Write([]byte("err\n"))
		}
		close(done)
	}()
	for i := 0; i < 200; i++ {
		stdout.Write([]byte("out\n"))
	}
	<-done
	if log.Len() != 200*8 {
		t.Fatalf("log has %d bytes", log.Len())
	}
}
