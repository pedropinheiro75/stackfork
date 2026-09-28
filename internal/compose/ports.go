package compose

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PortRef names one service's target port in an isolated Compose model.
type PortRef struct {
	Service string
	Target  int
}

// PublishedPorts scans the isolated model's services for every ports-list
// entry and returns them sorted by service then target (task 8.2, design.md
// D7 layer 2): each one is published to a host port by Docker, whether or
// not the source pinned an explicit "published" value, and Isolate always
// strips that value so Docker would otherwise pick a new random one on every
// container creation. These are the ones prepare gives a stable host port
// to. Isolate has already refused any entry with an unresolved target, so
// every remaining entry here has a valid int target.
func PublishedPorts(model interface{}) []PortRef {
	m, ok := asMap(model)
	if !ok {
		return nil
	}
	services, _ := asMap(m["services"])
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []PortRef
	for _, name := range names {
		svc, ok := asMap(services[name])
		if !ok {
			continue
		}
		portsList, ok := asList(svc["ports"])
		if !ok {
			continue
		}
		for _, raw := range portsList {
			port, ok := asMap(raw)
			if !ok {
				continue
			}
			n, ok := port["target"].(json.Number)
			if !ok {
				continue
			}
			target, err := n.Int64()
			if err != nil {
				continue
			}
			out = append(out, PortRef{Service: name, Target: int(target)})
		}
	}
	return out
}

// ApplyPortAllocations writes each allocation's host port into the isolated
// model's matching ports-list entry: "published" (a string, as Compose's
// JSON config represents it) plus host_ip 127.0.0.1, so `docker compose up`
// binds the same port every time (task 8.2, design.md D7 layer 2). Call it
// after PublishedPorts on the same model.
func ApplyPortAllocations(model interface{}, hostPorts map[PortRef]int) {
	if len(hostPorts) == 0 {
		return
	}
	m, ok := asMap(model)
	if !ok {
		return
	}
	services, _ := asMap(m["services"])
	for svcName, raw := range services {
		svc, ok := asMap(raw)
		if !ok {
			continue
		}
		portsList, ok := asList(svc["ports"])
		if !ok {
			continue
		}
		for _, rawPort := range portsList {
			port, ok := asMap(rawPort)
			if !ok {
				continue
			}
			n, ok := port["target"].(json.Number)
			if !ok {
				continue
			}
			target, err := n.Int64()
			if err != nil {
				continue
			}
			host, ok := hostPorts[PortRef{Service: svcName, Target: int(target)}]
			if !ok {
				continue
			}
			port["published"] = strconv.Itoa(host)
			port["host_ip"] = "127.0.0.1"
		}
	}
}

// RefusePublishedReplicas refuses when a service publishes a host port
// (named in refs, from PublishedPorts) while running more than one replica:
// Docker cannot bind two containers to the same host port (task 8.4,
// design.md D7 layer 2). The template's own `app` service keeps working
// because its ports all sit behind nginx, unpublished - only nginx and db
// publish, and neither scales.
func RefusePublishedReplicas(model interface{}, refs []PortRef) error {
	m, ok := asMap(model)
	if !ok {
		return nil
	}
	services, _ := asMap(m["services"])

	published := map[string]bool{}
	for _, r := range refs {
		published[r.Service] = true
	}
	names := make([]string, 0, len(published))
	for name := range published {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc, ok := asMap(services[name])
		if !ok {
			continue
		}
		count, resolved := replicaCount(svc)
		if !resolved {
			return fmt.Errorf("services.%s: deploy.replicas is not a resolved number (design.md D6: --no-interpolate keeps an unresolved ${VAR} literal) and this service also publishes a host port; cannot confirm it isn't scaled to more than one replica without guessing", name)
		}
		if count > 1 {
			return fmt.Errorf("services.%s: cannot publish a host port with more than one replica; put it behind a proxy service instead", name)
		}
	}
	return nil
}

// replicaCount reads a service's deploy.replicas, defaulting to (1, true)
// when unset. resolved is false only when replicas is set but couldn't be
// parsed as a number *because* it's still literally "${VAR}" (design.md D6:
// --no-interpolate never resolves that from prepare's own environment) -
// the caller must refuse rather than guess 1, since the real value could
// turn out to be > 1 once actually interpolated at up time. Any other
// unparsable value (a typo, a genuinely empty string) still defaults to 1,
// unchanged from before this distinction existed.
func replicaCount(svc map[string]interface{}) (n int, resolved bool) {
	deploy, ok := asMap(svc["deploy"])
	if !ok {
		return 1, true
	}
	raw, present := deploy["replicas"]
	if !present {
		return 1, true
	}
	if num, ok := raw.(json.Number); ok {
		v, err := num.Int64()
		if err != nil || v < 1 {
			return 1, true
		}
		return int(v), true
	}
	if s, ok := raw.(string); ok && strings.Contains(s, "${") {
		return 0, false
	}
	return 1, true
}
