package dashboard

import (
	"fmt"
	"strings"
)

// What the VersionCard shows of the instance's last run (design.md D14).
// app.js's versionHtml makes the same decisions from the same last_run JSON;
// change both together.

// runView is the callout of a failed or interrupted run.
type runView struct {
	Tone, Glyph string // failed ⊘, or attention ▲
	Title       string
	Step, Tail  string
	Run         int
	Retry       string // a command /api/run runs; empty when Retry starts the checkout again
	RetryStart  bool   // Retry sends the Repos Start action for Checkout
	CanRetry    bool
	Repo        string // main checkout of the repo, for the Start action
}

type lastRunMenu struct {
	Run  int
	Text string // succeeded · 41 s · 5 min ago
}

// isStartEnvRetry tells a retry that repeats the whole Start from one that
// repeats a single command (seed, up).
func isStartEnvRetry(cmd string) bool {
	return strings.HasPrefix(cmd, "stackfork --root ") && strings.HasSuffix(cmd, " start-env")
}

// RunStarting is the Starting… chip that replaces the state chip while a run
// is in progress, whoever started it.
func (c cardData) RunStarting() *chipView {
	lr := c.LastRun
	if lr == nil || lr.Outcome != "running" {
		return nil
	}
	title := "Start in progress"
	if lr.Step != "" {
		title = fmt.Sprintf("step %d of %d: %s", lr.StepN, lr.StepTotal, lr.Step)
	}
	return &chipView{Class: "busy", Glyph: glyphBusy, Label: "Starting…", Title: title}
}

// seedCovers is true when the seed callout already reports the failure of the
// last run, so the card never shows two callouts for one failure.
func (c cardData) seedCovers() bool {
	lr := c.LastRun
	return lr != nil && lr.Outcome == "failed" && c.SeedCommand != "" && lr.FailedStep != "" && c.SeedRun == lr.ID && c.SeedStep == lr.FailedStep
}

// RunCallout is nil unless the last run failed or was interrupted.
func (c cardData) RunCallout() *runView {
	lr := c.LastRun
	if lr == nil || (lr.Outcome != "failed" && lr.Outcome != "interrupted") || c.seedCovers() {
		return nil
	}
	v := &runView{Tone: "failed", Glyph: glyphFailed, Title: "Start failed", Step: lr.FailedStep, Tail: lr.Tail, Run: lr.ID}
	if lr.Outcome == "interrupted" {
		v.Tone, v.Glyph, v.Title = "attention", glyphAttention, "Start interrupted"
	}
	if lr.FailedStep != "" {
		v.Title += " at step"
	}
	switch {
	case lr.Retry == "":
	case isStartEnvRetry(lr.Retry):
		v.CanRetry, v.RetryStart, v.Repo = true, true, repoRoot(c.Root)
	default:
		v.CanRetry, v.Retry = true, lr.Retry
	}
	return v
}

// LastRunMenu is non-nil for a succeeded last run: the version menu's Last run item.
func (c cardData) LastRunMenu() *lastRunMenu {
	lr := c.LastRun
	if lr == nil || lr.Outcome != "succeeded" {
		return nil
	}
	return &lastRunMenu{Run: lr.ID, Text: "succeeded · " + durText(lr.EndedAt.Sub(lr.StartedAt).Milliseconds()) + " · " + ago(lr.StartedAt)}
}

// durText must match app.js fmtDur: 320 ms, 41 s, 1 m 5 s.
func durText(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	s := (ms + 500) / 1000
	if s < 60 {
		return fmt.Sprintf("%d s", s)
	}
	return fmt.Sprintf("%d m %d s", s/60, s%60)
}
