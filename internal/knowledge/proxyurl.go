package knowledge

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// ProxyURL is the portless address the shared proxy serves an instance on.
// The one rule the dashboard links, `urls` and the {{url}} placeholders share,
// so they never disagree.
func ProxyURL(hostname string, https bool, proxyPort int) string {
	u := fmt.Sprintf("%s://%s", scheme(https), hostname)
	if proxyPort != 80 {
		u += ":" + strconv.Itoa(proxyPort)
	}
	return u
}

// ProxyEntryApp is the host app the proxy routes for an instance: the one
// PickHostApp chooses among the plan's host apps and the instance's recorded
// ones, so the answer is the same before and after they run. "" when there
// are none. info may be nil.
func ProxyEntryApp(k *Knowledge, info *state.Manifest) string {
	routes := map[string]int{}
	var apps map[string]HostApp
	if k != nil {
		for _, s := range k.Plan.HostSteps() {
			if s.Name != "" {
				routes[s.Name] = 0
			}
		}
	}
	if info != nil {
		for n := range info.Routes {
			routes[n] = 0
		}
		for n := range info.HostPorts {
			routes[n] = 0
		}
		apps = HostApps(info)
		for n := range apps {
			routes[n] = 0
		}
	}
	return PickHostApp(k, routes, apps)
}

// UsesProxyURL reports whether argv has a placeholder whose value changes
// with the proxy: {{url}} of the entry app itself, {{ENTRY.url}}, or any
// {{service.NAME.url}} (ponytail: a service counts even when the proxy does
// not route it; one restart too many beats a stale URL).
func UsesProxyURL(argv []string, self, entry string) bool {
	for _, a := range argv {
		for _, m := range placeholderRE.FindAllStringSubmatch(a, -1) {
			switch name := m[1]; {
			case name == "url":
				if self != "" && self == entry {
					return true
				}
			case strings.HasPrefix(name, "service."):
				if strings.HasSuffix(name, ".url") {
					return true
				}
			case entry != "" && name == entry+".url":
				return true
			}
		}
	}
	return false
}
