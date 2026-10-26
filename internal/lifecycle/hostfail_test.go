package lifecycle

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// A host app that fails to start fails the run and names the step; the other
// host steps still start and keep their own states.
func TestFailedHostStepFailsTheRunAndOthersStillRun(t *testing.T) {
	dir := t.TempDir()
	rec, err := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli", Mode: knowledge.ModeHybrid, Project: "p", Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	var started []string
	runErr := PlanRunner{Out: io.Discard, Err: io.Discard, Exact: true, InstanceDir: dir, Mode: knowledge.ModeHybrid, Run: rec,
		Plan: knowledge.Plan{Steps: []knowledge.Step{
			{ID: "api", Kind: knowledge.KindHost, Name: "api", Cmd: "x"},
			{ID: "app", Kind: knowledge.KindHost, Name: "app", Cmd: "x"},
			{ID: "studio", Kind: knowledge.KindHost, Name: "studio", Cmd: "x"},
		}},
		Host: func(w io.Writer, s knowledge.Step) error {
			started = append(started, s.ID)
			if s.ID == "api" {
				return errors.New("host process api exited at start:\nERR_MODULE_NOT_FOUND")
			}
			return nil
		}}.Execute()
	rec.Finish(runErr)
	if runErr == nil || !strings.Contains(runErr.Error(), "api") || !strings.Contains(runErr.Error(), "ERR_MODULE_NOT_FOUND") {
		t.Fatalf("error: %v", runErr)
	}
	if strings.Join(started, ",") != "api,app,studio" {
		t.Fatalf("started %v", started)
	}
	r, err := runs.Get(dir, 1)
	if err != nil || r.Outcome != runs.Failed {
		t.Fatalf("%+v %v", r, err)
	}
	got := ""
	for _, s := range r.Steps {
		got += s.ID + "=" + s.State + " "
	}
	if got != "api=failed app=succeeded studio=succeeded " {
		t.Fatalf("steps: %s", got)
	}
}
