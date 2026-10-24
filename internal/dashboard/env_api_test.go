package dashboard_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

type envReq struct {
	Project string            `json:"project"`
	Service string            `json:"service,omitempty"`
	Set     map[string]string `json:"set,omitempty"`
	Unset   []string          `json:"unset,omitempty"`
	Reset   bool              `json:"reset,omitempty"`
}

type envResp struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Run     int    `json:"run"`
}

func envServer(t *testing.T, port int) (srv *dashboard.Server, host, project, dir string) {
	t.Helper()
	newBase, legacyBase, project := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", port)
	if err != nil {
		t.Fatal(err)
	}
	return srv, "127.0.0.1:" + itoa(port), project, filepath.Join(newBase, project)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func postEnv(t *testing.T, srv *dashboard.Server, host string, body envReq, tweak func(*http.Request)) (int, envResp, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/env", bytes.NewReader(b))
	req.Host = host
	req.Header.Set("X-Stackfork-Token", srv.Token)
	req.Header.Set("Origin", "http://"+host)
	req.Header.Set("Content-Type", "application/json")
	if tweak != nil {
		tweak(req)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp envResp
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp, rec.Body.String()
}

func TestEnvPostRefusesWithoutTokenOrOrigin(t *testing.T) {
	srv, host, project, dir := envServer(t, 55401)
	body := envReq{Project: project, Service: "db", Set: map[string]string{"ENABLE_X": "false"}}
	if code, _, _ := postEnv(t, srv, host, body, func(r *http.Request) { r.Header.Del("X-Stackfork-Token") }); code != 403 {
		t.Errorf("no token: %d", code)
	}
	if code, _, _ := postEnv(t, srv, host, body, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }); code != 403 {
		t.Errorf("bad origin: %d", code)
	}
	if code, _, _ := postEnv(t, srv, host, body, func(r *http.Request) { r.Header.Del("Origin") }); code != 403 {
		t.Errorf("no origin: %d", code)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("a refused request wrote an override")
	}
}

func TestEnvPostSetAppliesOnlyThatServiceAsOneRun(t *testing.T) {
	srv, host, project, dir := envServer(t, 55402)
	code, resp, raw := postEnv(t, srv, host, envReq{Project: project, Service: "db", Set: map[string]string{"ENABLE_X": "false"}}, nil)
	if code != 200 || !resp.OK || resp.Run == 0 {
		t.Fatalf("%d %s", code, raw)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Services["db"]["ENABLE_X"] != "false" {
		t.Fatalf("not stored: %+v", o)
	}
	r, err := runs.Get(dir, resp.Run)
	if err != nil || r.Command != "env" || len(r.Steps) != 1 || !strings.Contains(r.Steps[0].Text, "db") {
		t.Fatalf("run: %+v %v", r, err)
	}
	// unset restores the project default
	code, resp, raw = postEnv(t, srv, host, envReq{Project: project, Service: "db", Unset: []string{"ENABLE_X"}}, nil)
	if code != 200 || !resp.OK {
		t.Fatalf("unset: %d %s", code, raw)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Errorf("not removed: %+v", o)
	}
}

func TestEnvPostRefusesSecretLikeNamesPointingToSecrets(t *testing.T) {
	srv, host, project, dir := envServer(t, 55403)
	code, resp, _ := postEnv(t, srv, host, envReq{Project: project, Service: "db", Set: map[string]string{"API_TOKEN": "hunter2"}}, nil)
	if code != 400 || resp.OK || !strings.Contains(resp.Message, "Settings → Secrets") {
		t.Fatalf("%d %+v", code, resp)
	}
	if strings.Contains(resp.Message, "hunter2") {
		t.Error("the message echoes the value")
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("a secret-like name was stored")
	}
}

func TestEnvPostRejectsBadInput(t *testing.T) {
	srv, host, project, _ := envServer(t, 55404)
	for name, body := range map[string]envReq{
		"unknown project": {Project: "sf-nope-1", Service: "db", Set: map[string]string{"A": "1"}},
		"unknown service": {Project: project, Service: "zzz", Set: map[string]string{"A": "1"}},
		"bad name":        {Project: project, Service: "db", Set: map[string]string{"1A": "1"}},
		"nothing to do":   {Project: project, Service: "db"},
		"no service":      {Project: project, Set: map[string]string{"A": "1"}},
	} {
		if code, resp, _ := postEnv(t, srv, host, body, nil); code != 400 || resp.OK {
			t.Errorf("%s: %d %+v", name, code, resp)
		}
	}
}

func TestEnvResetClearsAllAndRunsOneStepPerService(t *testing.T) {
	srv, host, project, dir := envServer(t, 55405)
	if err := lifecycle.SetEnvOverrides(dir, "db", map[string]string{"A": "1"}, nil, false); err != nil {
		t.Fatal(err)
	}
	code, resp, raw := postEnv(t, srv, host, envReq{Project: project, Reset: true}, nil)
	if code != 200 || !resp.OK || resp.Run == 0 {
		t.Fatalf("%d %s", code, raw)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Errorf("not cleared: %+v", o)
	}
	r, _ := runs.Get(dir, resp.Run)
	if r == nil || r.Command != "env" || len(r.Steps) != 1 {
		t.Fatalf("one run, one step per touched service: %+v", r)
	}
	// nothing left to reset
	if code, resp, _ := postEnv(t, srv, host, envReq{Project: project, Reset: true}, nil); code != 400 || resp.OK {
		t.Errorf("empty reset: %d %+v", code, resp)
	}
}

// D9: a running up makes the dashboard answer busy and write nothing.
func TestEnvPostIsBusyWhileARunOwnsTheInstance(t *testing.T) {
	srv, host, project, dir := envServer(t, 55406)
	rec, err := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	code, resp, _ := postEnv(t, srv, host, envReq{Project: project, Service: "db", Set: map[string]string{"A": "1"}}, nil)
	rec.Finish(nil)
	if code != http.StatusConflict || resp.OK || !strings.Contains(resp.Message, "busy") {
		t.Fatalf("%d %+v", code, resp)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("busy request wrote an override")
	}
}

func TestEnvPostIsBusyWhileTheDashboardRunsAnUp(t *testing.T) {
	srv, host, project, dir := envServer(t, 55407)
	t.Setenv("DOCKER_FAKE_SLEEP", "0.5")
	done := make(chan int, 1)
	go func() {
		code, _ := postAction(t, srv, host, actionRequestFixture{Action: "up", Project: project})
		done <- code
	}()
	time.Sleep(100 * time.Millisecond)
	code, resp, _ := postEnv(t, srv, host, envReq{Project: project, Service: "db", Set: map[string]string{"A": "1"}}, nil)
	if code != http.StatusConflict || !strings.Contains(resp.Message, "busy") {
		t.Fatalf("%d %+v", code, resp)
	}
	<-done
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("busy request wrote an override")
	}
}

func TestEnvGetNeverCarriesAMaskedValue(t *testing.T) {
	srv, host, project, dir := envServer(t, 55408)
	if err := lifecycle.SetEnvOverrides(dir, "db", map[string]string{"API_TOKEN": "s3cr3t-value", "ENABLE_X": "true"}, nil, true); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(srv, "GET", "/api/env?project="+project, host, nil)
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "s3cr3t-value") {
		t.Fatalf("%d %s", rec.Code, body)
	}
	var got struct {
		Services []lifecycle.EnvService `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Services) == 0 {
		t.Fatalf("%v %s", err, body)
	}
	var tok *lifecycle.EnvVar
	for i, v := range got.Services[0].Vars {
		if v.Name == "API_TOKEN" {
			tok = &got.Services[0].Vars[i]
		}
	}
	if tok == nil || !tok.Masked || tok.Value != nil {
		t.Fatalf("API_TOKEN: %+v", tok)
	}
	if rec := doRequest(srv, "GET", "/api/env?project=nope", host, nil); rec.Code != 400 {
		t.Errorf("bad project: %d", rec.Code)
	}
}
