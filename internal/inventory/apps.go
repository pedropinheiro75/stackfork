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
	Service string `json:"service,omitempty"` // the service a Source "service" link belongs to
	URL     string `json:"url,omitempty"`
	Direct  string `json:"direct,omitempty"`
	Source  string `json:"source"` // service | route | knowledge
	Running bool   `json:"running"`
	Hint    string `json:"hint,omitempty"`
}

// buildApps collects every service-sourced app link: one per distinct
// container port with an HTTP(S) endpoint, labelled by role - never two
// chips for the same port (maintainer feedback: the primary service's own
// direct address and the proxy's portless one for that exact port were
// rendering as two identically-labelled "App" chips). proxyEndpoint always
// gives its extra entry the same Container as the direct one it stands in
// for, so grouping by Container safely collapses exactly that pair - a
// service that genuinely declares two different HTTP ports still gets two
// chips, one per port. Within a group the proxied URL wins (D3b wants the
// portless one shown first); the direct one, if any, rides along as
// AppLink.Direct for the template to show as a tooltip/secondary link,
// never a second chip.
func buildApps(services []Service, info *state.Manifest, primaryService string, hasPrimary bool) []AppLink {
	var apps []AppLink
	for _, svc := range services {
		label := appLabel(svc, hasPrimary && svc.Name == primaryService)
		byTarget := map[int][]Endpoint{}
		var targets []int
		for _, ep := range svc.Endpoints {
			if ep.Scheme != "http" && ep.Scheme != "https" {
				continue
			}
			if _, seen := byTarget[ep.Container]; !seen {
				targets = append(targets, ep.Container)
			}
			byTarget[ep.Container] = append(byTarget[ep.Container], ep)
		}
		for _, target := range targets {
			group := byTarget[target]
			sort.SliceStable(group, func(i, j int) bool {
				return group[i].Proxied && !group[j].Proxied
			})
			best := group[0]
			hint := ""
			if !best.Known {
				hint = "port assigned on start"
			}
			link := AppLink{
				Label: label, Service: svc.Name, URL: best.URL, Source: "service",
				Running: svc.State == "running", Hint: hint,
			}
			if len(group) > 1 && group[1].URL != best.URL {
				link.Direct = group[1].URL
			}
			apps = append(apps, link)
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

// buildEnvBlock assembles the "Copy .env for host app" block (task 3.10,
// design.md D3b §4, extended by add-env-and-seed for `stackfork env`): one
// line per qualifying kind, first match wins if a stack somehow has more
// than one of the same kind (ponytail: good enough - a stack with two
// Postgres services is unusual, and the App strip/per-service copy
// buttons still cover the second one individually). Also one
// <SERVICE>_URL line per HTTP(S)-serving service, so a host-run app gets
// its sibling containers' addresses the same way it gets DATABASE_URL -
// the exact URL buildApps would show as that service's App chip
// (bestHTTPEndpoint shares its proxied-preferred rule).
func buildEnvBlock(services []Service) string {
	var lines []string
	have := map[string]bool{}
	add := func(key, value string) {
		if value == "" || have[key] {
			return
		}
		have[key] = true
		lines = append(lines, key+"="+value)
	}
	for _, svc := range services {
		switch svc.Kind {
		case "postgres", "mysql", "mariadb":
			add("DATABASE_URL", svc.ConnectionURL)
		case "redis":
			add("REDIS_URL", svc.ConnectionURL)
		case "mongo":
			add("MONGODB_URL", svc.ConnectionURL)
		case "mail":
			for _, c := range svc.UseCommands {
				if c.Label == "SMTP_HOST" || c.Label == "SMTP_PORT" {
					add(c.Label, c.Value)
				}
			}
		}
		if ep, ok := bestHTTPEndpoint(svc); ok {
			add(envKeyForService(svc.Name)+"_URL", ep.URL)
		}
	}
	return strings.Join(lines, "\n")
}

// bestHTTPEndpoint returns a service's most useful HTTP(S) address: when
// the proxy is routing the service's first declared HTTP port, the
// portless proxied one wins over the plain direct one for that exact
// port (buildApps's own pairing rule, shared here rather than
// reimplemented). A service with more than one distinct HTTP port only
// ever contributes its first here - buildApps still gives each port its
// own App chip; env's one-line-per-service scope doesn't need to.
func bestHTTPEndpoint(svc Service) (Endpoint, bool) {
	var group []Endpoint
	firstTarget := -1
	for _, ep := range svc.Endpoints {
		if ep.Scheme != "http" && ep.Scheme != "https" {
			continue
		}
		if firstTarget == -1 {
			firstTarget = ep.Container
		}
		if ep.Container == firstTarget {
			group = append(group, ep)
		}
	}
	if len(group) == 0 {
		return Endpoint{}, false
	}
	sort.SliceStable(group, func(i, j int) bool { return group[i].Proxied && !group[j].Proxied })
	return group[0], true
}

// envKeyForService uppercases a service name into a valid shell/dotenv
// identifier - Compose service names are conventionally already
// alnum/hyphen (docker-compose.yml's own service-name schema), but this
// never assumes it: anything else becomes "_", and a leading digit gets a
// "_" prefix so the result is always a legal env var name.
func envKeyForService(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	key := b.String()
	if key == "" {
		return "_"
	}
	if key[0] >= '0' && key[0] <= '9' {
		key = "_" + key
	}
	return key
}
