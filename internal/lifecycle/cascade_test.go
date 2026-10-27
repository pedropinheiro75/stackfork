package lifecycle

import (
	"reflect"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func hostStep(name string, deps ...string) knowledge.Step {
	return knowledge.Step{ID: name, Kind: knowledge.KindHost, Name: name, Cmd: "x", DependsOn: deps}
}

func TestDependentsTransitiveAndMixed(t *testing.T) {
	deps := map[string][]string{"postgres": nil, "redis": nil, "worker": {"postgres", "redis"}, "report": {"worker"}, "cache": nil}
	plan := knowledge.Plan{Steps: []knowledge.Step{
		hostStep("api", "postgres"),
		hostStep("app", "api"),                  // a host app on a host app
		hostStep("studio", "host:app", "ghost"), // an unknown name is ignored
		hostStep("other"),
		hostStep("cacheui", "cache"),
	}}
	svcs, hosts := Dependents(plan, deps, []string{"postgres"})
	if !reflect.DeepEqual(svcs, []string{"report", "worker"}) || !reflect.DeepEqual(hosts, []string{"api", "app", "studio"}) {
		t.Fatalf("postgres: %v %v", svcs, hosts)
	}
	// a host app is a target too, and only hosts depend on it
	svcs, hosts = Dependents(plan, deps, []string{"host:api"})
	if len(svcs) != 0 || !reflect.DeepEqual(hosts, []string{"app", "studio"}) {
		t.Fatalf("host:api: %v %v", svcs, hosts)
	}
	if svcs, hosts = Dependents(plan, deps, []string{"redis"}); !reflect.DeepEqual(svcs, []string{"report", "worker"}) || len(hosts) != 0 {
		t.Fatalf("redis: %v %v", svcs, hosts)
	}
	if svcs, hosts = Dependents(plan, deps, []string{"host:other"}); len(svcs)+len(hosts) != 0 {
		t.Fatalf("a leaf has no dependents: %v %v", svcs, hosts)
	}
}

func TestDependentsCyclesEnd(t *testing.T) {
	deps := map[string][]string{"a": {"b"}, "b": {"a"}, "c": {"a"}}
	plan := knowledge.Plan{Steps: []knowledge.Step{hostStep("x", "host:y", "a"), hostStep("y", "host:x")}}
	svcs, hosts := Dependents(plan, deps, []string{"a"})
	if !reflect.DeepEqual(svcs, []string{"b", "c"}) || !reflect.DeepEqual(hosts, []string{"x", "y"}) {
		t.Fatalf("%v %v", svcs, hosts)
	}
	if _, hosts = Dependents(plan, deps, []string{"host:x"}); !reflect.DeepEqual(hosts, []string{"y"}) {
		t.Fatalf("the target is not its own dependent: %v", hosts)
	}
}

func TestCascadeLine(t *testing.T) {
	if got := cascadeLine([]string{"postgres"}, []string{"api", "app"}); got != "stopped postgres and its dependents: api, app" {
		t.Fatal(got)
	}
	if got := cascadeLine([]string{"a", "b"}, []string{"c"}); got != "stopped a, b and their dependents: c" {
		t.Fatal(got)
	}
}
