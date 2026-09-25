package inventory

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// AppLink is one way to open or reach a stack's app (design.md D3b, task
// 3.8): the page's whole point is to open and use the app, not inspect
// containers.
type AppLink struct {
	Label   string `json:"label"`
	URL     string `json:"url,omitempty"`
	Source  string `json:"source"` // service | route | knowledge
	Running bool   `json:"running"`
	Hint    string `json:"hint,omitempty"`
}

// buildApps collects every service-sourced app link: one per HTTP(S)
// endpoint, labelled by role. A service's endpoints already carry a
// portless, proxied one last (buildServiceEndpoints/proxyEndpoint); D3b
// wants that one shown first, so it's resorted here without touching
// Service.Endpoints itself (other code, and the golden test, depend on
// that order staying as-is).
func buildApps(services []Service, info *state.Manifest, primaryService string, hasPrimary bool) []AppLink {
	var apps []AppLink
	for _, svc := range services {
		label := appLabel(svc, hasPrimary && svc.Name == primaryService)
		httpEndpoints := make([]Endpoint, 0, len(svc.Endpoints))
		for _, ep := range svc.Endpoints {
			if ep.Scheme == "http" || ep.Scheme == "https" {
				httpEndpoints = append(httpEndpoints, ep)
			}
		}
		sort.SliceStable(httpEndpoints, func(i, j int) bool {
			return httpEndpoints[i].Proxied && !httpEndpoints[j].Proxied
		})
		for _, ep := range httpEndpoints {
			hint := ""
			if !ep.Known {
				hint = "port assigned on start"
			}
			apps = append(apps, AppLink{
				Label: label, URL: ep.URL, Source: "service",
				Running: svc.State == "running", Hint: hint,
			})
		}
	}
	return apps
}

// appLabel picks the App/Mail inbox/DB admin/service-name role D3b §1
// describes, from the service's own (already-computed) Kind and image, so
// it never needs its own image-parsing pass.
func appLabel(svc Service, isPrimary bool) string {
	switch {
	case isPrimary:
		return "App"
	case svc.Kind == "mail":
		return "Mail inbox"
	}
	image := strings.ToLower(svc.Image)
	if strings.Contains(image, "adminer") || strings.Contains(image, "pgadmin") {
		return "DB admin"
	}
	return svc.Name
}

// appendRouteAndKnowledgeApps adds host-run route links (design.md D3b §2:
// "manifest routes render as App chips") after the service-sourced ones,
// and - only when nothing else in the strip would give the user anything to
// open - a hint built from a learned dev script. That's the real gap this
// fixes: a checkout whose app runs on the host has no service endpoint and,
// before `route add`, no route either, so without this the App strip would
// be silently empty even though `stackfork learn` knows how to start it.
//
// ponytail: knowledge.Script has no recorded port (the schema was never
// extended to track one), so unlike a route this can only ever suggest a
// command, never a clickable link or a filled-in `route add <port>` - the
// upgrade path is teaching `learn` to capture a dev script's port.
func appendRouteAndKnowledgeApps(apps []AppLink, info *state.Manifest) []AppLink {
	if len(info.Routes) > 0 {
		names := make([]string, 0, len(info.Routes))
		for name := range info.Routes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			apps = append(apps, AppLink{
				Label: "App", URL: fmt.Sprintf("http://127.0.0.1:%d", info.Routes[name]),
				Source: "route", Running: true,
			})
		}
	}

	if len(apps) > 0 {
		return apps
	}
	k, err := knowledge.Load(knowledgeJSONPath(info.Root))
	if err != nil {
		return apps
	}
	for _, s := range k.Facts.Scripts {
		if s.Role != "dev" {
			continue
		}
		cmd := s.Command
		if cmd == "" {
			cmd = s.Name
		}
		apps = append(apps, AppLink{
			Label:  "App",
			Source: "knowledge",
			Hint: fmt.Sprintf("host app: not running — start with `%s`, then `stackfork route add <name> <port>`",
				cmd),
		})
	}
	return apps
}

func knowledgeJSONPath(root string) string {
	return filepath.Join(root, ".stackfork", "knowledge.json")
}
