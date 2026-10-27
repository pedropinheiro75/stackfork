package lifecycle

import (
	"reflect"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestProxyTargetsPicksRunningAppsWithProxyURLs(t *testing.T) {
	plan := knowledge.Plan{Steps: []knowledge.Step{
		{ID: "api", Kind: knowledge.KindHost, Name: "api", Cmd: "env UI_URL={{app.url}} node api"},
		{ID: "app", Kind: knowledge.KindHost, Name: "app", Cmd: "env API={{api.url}} vite --port {{port}}"},
		{ID: "wk", Kind: knowledge.KindHost, Name: "worker", Cmd: "node worker {{app.url}}"},
	}}
	k := &knowledge.Knowledge{Plan: plan}
	m := &state.Manifest{HostProcs: map[string]state.HostProc{"api": {PID: 1}, "app": {PID: 2}, "worker": {PID: 3}}}
	names := func(running func(state.HostProc) bool) []string {
		var out []string
		for _, t := range proxyTargets(m, k, plan, knowledge.ModeHybrid, running) {
			out = append(out, t.Name)
		}
		return out
	}
	// app is the served app: api and worker use its URL, app only uses api's.
	if got := names(func(state.HostProc) bool { return true }); !reflect.DeepEqual(got, []string{"api", "worker"}) {
		t.Fatalf("got %v", got)
	}
	// a stopped app is left alone
	if got := names(func(p state.HostProc) bool { return p.PID != 1 }); !reflect.DeepEqual(got, []string{"worker"}) {
		t.Fatalf("got %v", got)
	}
}

func TestProxyStaleRecord(t *testing.T) {
	dir := t.TempDir()
	state.SetProxyStale(dir, []string{"api"})
	// a process with no identity is not "still running from before"
	if got := state.ProxyStaleApps(dir, map[string]state.HostProc{"api": {PID: 1}}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	state.SetProxyStale(dir, nil)
}
