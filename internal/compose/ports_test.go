package compose

import "testing"

func TestPublishedPortsListsEveryPortsEntry(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {
			"nginx": {"ports": [{"target": 3000, "host_ip": "127.0.0.1", "mode": "ingress"}]},
			"app":   {"deploy": {"replicas": 2}},
			"db":    {"ports": [{"target": 5432, "host_ip": "127.0.0.1", "mode": "ingress"}]}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	refs := PublishedPorts(model)
	want := []PortRef{{Service: "db", Target: 5432}, {Service: "nginx", Target: 3000}}
	if len(refs) != len(want) {
		t.Fatalf("expected %v, got %v", want, refs)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, refs)
		}
	}
}

func TestApplyPortAllocationsSetsPublishedAndHostIP(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {"db": {"ports": [{"target": 5432, "mode": "ingress"}]}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	ApplyPortAllocations(model, map[PortRef]int{{Service: "db", Target: 5432}: 20017})

	m, _ := asMap(model)
	services, _ := asMap(m["services"])
	db, _ := asMap(services["db"])
	ports, _ := asList(db["ports"])
	port, _ := asMap(ports[0])
	if port["published"] != "20017" {
		t.Fatalf("expected published \"20017\", got %v", port["published"])
	}
	if port["host_ip"] != "127.0.0.1" {
		t.Fatalf("expected host_ip 127.0.0.1, got %v", port["host_ip"])
	}
}

func TestRefusePublishedReplicasRefusesScaledPublishedService(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {"web": {"deploy": {"replicas": 3}, "ports": [{"target": 3000}]}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	refs := PublishedPorts(model)
	err = RefusePublishedReplicas(model, refs)
	if err == nil {
		t.Fatal("expected a refusal for a scaled service publishing a port")
	}
}

func TestRefusePublishedReplicasAllowsTemplateShape(t *testing.T) {
	// Mirrors assets/nuxt/compose.yaml: app scales but publishes nothing;
	// nginx publishes but doesn't scale (task 8.4).
	model, err := DecodeJSON([]byte(`{
		"services": {
			"app":   {"deploy": {"replicas": 2}},
			"nginx": {"ports": [{"target": 3000}]}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	refs := PublishedPorts(model)
	if err := RefusePublishedReplicas(model, refs); err != nil {
		t.Fatalf("expected no refusal, got %v", err)
	}
}
