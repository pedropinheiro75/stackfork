package dashboard_test

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func streamObjects(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not ndjson: %q", l)
		}
		out = append(out, m)
	}
	return out
}

// A Start that records a run streams run, step and tagged line objects, redacts
// the lines, and ends with the run id and the failed step (design D12).
func TestStartStreamCarriesTheRun(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.srv.StartEnv = func(out, errOut io.Writer, checkout, trigger string, obs func(runs.Event)) error {
		r, err := runs.Begin(t.TempDir(), runs.Meta{Command: "start-env", Trigger: trigger, Project: "acme-web-1", Out: out, Observer: obs,
			Secrets: func() []redact.Secret { return []redact.Secret{{Name: "TOKEN", Value: "s3cr3t-value-123"}} }})
		if err != nil {
			return err
		}
		prep, seed := r.Step("prepare", "prepare", "prepare"), r.Step("seed", "exec", "seed")
		r.Step("app", "host", "app")
		w := prep.Start()
		io.WriteString(w, "preparing\n")
		prep.End(nil, 0)
		w = seed.Start()
		io.WriteString(w, "using s3cr3t-value-123 now\nboom") // no newline: flushed at the step end
		err = errors.New("seed exited with status 1")
		seed.End(err, 1)
		r.Finish(err)
		return err
	}
	e.get("/api/repos")
	rec := e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	objs := streamObjects(t, rec.Body.String())
	var kinds []string
	for _, o := range objs {
		switch {
		case o["run"] != nil && o["ok"] == nil:
			kinds = append(kinds, "run")
		case o["step"] != nil && o["line"] == nil:
			s := o["step"].(map[string]any)
			kinds = append(kinds, "step:"+s["id"].(string)+":"+s["state"].(string))
		case o["line"] != nil:
			kinds = append(kinds, "line:"+o["step"].(string)+":"+o["line"].(string))
		default:
			kinds = append(kinds, "final")
		}
	}
	want := "run step:prepare:running line:prepare:preparing step:prepare:succeeded step:seed:running " +
		"line:seed:using [redacted:TOKEN] now line:seed:boom step:seed:failed final"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("got  %s\nwant %s\n%s", got, want, rec.Body.String())
	}
	run := objs[0]["run"].(map[string]any)
	steps := run["steps"].([]any)
	if run["id"] != float64(1) || run["project"] != "acme-web-1" || len(steps) != 3 || steps[1].(map[string]any)["kind"] != "exec" {
		t.Fatalf("%v", run)
	}
	final := objs[len(objs)-1]
	if final["ok"] != false || final["run"] != float64(1) || final["failed_step"] != "seed" {
		t.Fatalf("%v", final)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t-value-123") {
		t.Fatal("secret in the stream")
	}
}

// A Start that records nothing keeps the old two-shape stream.
func TestStartStreamWithoutRunIsUnchanged(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	rec := e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	for _, o := range streamObjects(t, rec.Body.String()) {
		if o["run"] != nil || o["step"] != nil || o["failed_step"] != nil {
			t.Fatalf("unexpected key: %v", o)
		}
	}
}
