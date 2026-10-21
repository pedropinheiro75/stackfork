package knowledge

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Entry sources (entry-links D6).
const (
	SourceHostRoute = "host-route"
	SourceRecorded  = "recorded"
	SourceGuessed   = "guessed"
	SourceNone      = "none"
)

// Resolved is the one answer to "where is the app" (entry-links D6): every
// consumer (proxy, inventory, urls, open, learn verify) reads it.
type Resolved struct {
	Source  string
	Service string // Compose service; "" for a host route or none
	Port    int    // container port; for a host route, the host port
	HTTPS   bool
	Path    string // "/" by default
	// OK is true when the entry has a target a URL can be built for. A
	// recorded target the stack lacks, `none` and a failed guess are not OK.
	OK      bool
	Warning string
	Links   []ResolvedLink
}

// ResolvedLink is one link with its target filled in. OnEntry marks a path
// on the entry's own base URL, the only kind the proxy's portless address
// can stand in for.
type ResolvedLink struct {
	Name    string
	Service string
	Port    int
	HTTPS   bool
	Path    string
	OnEntry bool
	OK      bool
	Warning string
}

// ResolveEntry applies the order of D6: a host route stands in for the
// entry's service and port, else a recorded entry (never replaced by a
// guess when the stack lacks it), else `none`, else the primary-service
// heuristic. k may be nil. hostRoutes is the manifest's Routes (name to
// host port); the first by name wins, as the proxy has always done.
func ResolveEntry(k *Knowledge, model interface{}, hostRoutes map[string]int) Resolved {
	var e *Entry
	var links []Link
	if k != nil {
		e, links = k.Entry, k.Links
	}
	httpPorts, httpsPorts := compose.DeclaredHTTPPorts(model)
	targets := compose.ServiceTargets(model)
	https := func(port int) bool {
		return compose.SchemeForTarget(port, "tcp", httpPorts, httpsPorts) == "https"
	}

	r := Resolved{Path: "/"}
	if e != nil && !e.None && e.Path != "" {
		r.Path = e.Path
	}
	switch {
	case len(hostRoutes) > 0:
		names := make([]string, 0, len(hostRoutes))
		for n := range hostRoutes {
			names = append(names, n)
		}
		sort.Strings(names)
		r.Source, r.Port, r.OK = SourceHostRoute, hostRoutes[names[0]], true
	case e != nil && e.None:
		r.Source = SourceNone
	case e != nil && !e.Proposed:
		r.Source, r.Service, r.Port = SourceRecorded, e.Service, e.Port
		if hasTarget(targets, e.Service, e.Port) {
			r.OK, r.HTTPS = true, https(e.Port)
		} else {
			r.Warning = fmt.Sprintf("the recorded entry %s:%d is not in this stack; record the right one with stackfork learn record entry", e.Service, e.Port)
		}
	default:
		r.Source = SourceGuessed
		if svc, port, isHTTPS, ok := compose.PrimaryHTTPService(model); ok {
			r.Service, r.Port, r.HTTPS, r.OK = svc, port, isHTTPS, true
		}
	}

	for _, l := range links {
		r.Links = append(r.Links, r.resolveLink(l, e, targets, https))
	}
	return r
}

func hasTarget(targets map[string][]int, service string, port int) bool {
	for _, p := range targets[service] {
		if p == port {
			return true
		}
	}
	return false
}

func (r Resolved) resolveLink(l Link, e *Entry, targets map[string][]int, https func(int) bool) ResolvedLink {
	rl := ResolvedLink{Name: l.Name, Service: l.Service, Port: l.Port, Path: l.Path}
	if rl.Path == "" {
		rl.Path = "/"
	}
	fix := "record the right one with stackfork learn record link"
	switch {
	case l.Service == "" && l.Port == 0: // a path on the entry
		rl.OnEntry = true
		switch {
		case e == nil || e.None:
			rl.Warning = fmt.Sprintf("link %q is a path on the entry, but no entry is recorded; record one with stackfork learn record entry", l.Name)
		case !r.OK:
			rl.Warning = fmt.Sprintf("link %q is a path on the entry, which is not usable; %s", l.Name, fix)
		default:
			rl.Service, rl.Port, rl.HTTPS, rl.OK = r.Service, r.Port, r.HTTPS, true
		}
		return rl
	case l.Service == "": // another port of the entry's service
		if r.Service == "" {
			rl.Warning = fmt.Sprintf("link %q names a port of the entry's service, which is not known here; %s", l.Name, fix)
			return rl
		}
		rl.Service = r.Service
	}
	if !hasTarget(targets, rl.Service, rl.Port) {
		rl.Warning = fmt.Sprintf("link %q targets %s:%d, which is not in this stack; %s", l.Name, rl.Service, rl.Port, fix)
		return rl
	}
	rl.OK, rl.HTTPS = true, https(rl.Port)
	return rl
}

// Where is what the URL builder needs to know about one instance (D7).
type Where struct {
	Hostname string
	// Rows are the instance's `docker compose ps` rows; Stable is the
	// manifest's port allocations, the fallback while a service is stopped.
	Rows   []compose.StatusRow
	Stable []state.PortAlloc
	// ProxyPort is the shared proxy's port when it is running and routes
	// this instance, else 0.
	ProxyPort int
}

func (w Where) hostPort(service string, port int) int {
	if p, ok := compose.PublishedHostPort(w.Rows, service, port); ok {
		return p
	}
	for _, a := range w.Stable {
		if a.Service == service && a.Target == port && a.Host > 0 {
			return a.Host
		}
	}
	return 0
}

func scheme(https bool) string {
	if https {
		return "https"
	}
	return "http"
}

func suffix(path string) string {
	if path == "/" {
		return ""
	}
	return path
}

// direct is the URL on the instance's own published port; "" when the port
// is not known yet (a random port on a stopped service).
func (r Resolved) direct(w Where, service string, port int, https bool, path string, host bool) string {
	if host {
		return fmt.Sprintf("http://127.0.0.1:%d%s", port, suffix(path))
	}
	p := w.hostPort(service, port)
	if p == 0 {
		return ""
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme(https), w.Hostname, p, suffix(path))
}

func (r Resolved) proxied(w Where, https bool, path string) string {
	u := fmt.Sprintf("%s://%s", scheme(https), w.Hostname)
	if w.ProxyPort != 80 {
		u += ":" + strconv.Itoa(w.ProxyPort)
	}
	return u + suffix(path)
}

// AppURL is the entry's address: the proxy's portless URL when the proxy
// routes the instance (direct then carries the published-port URL), else
// the direct URL. Both are "" when the entry is not OK or its port is not
// known.
func (r Resolved) AppURL(w Where) (url, direct string) {
	if !r.OK {
		return "", ""
	}
	direct = r.direct(w, r.Service, r.Port, r.HTTPS, r.Path, r.Source == SourceHostRoute)
	if w.ProxyPort > 0 {
		return r.proxied(w, r.HTTPS, r.Path), direct
	}
	return direct, ""
}

// LinkURL is a link's address. Only a path on the entry goes through the
// proxy; a link to a service always uses its direct URL (the proxy routes
// one service per hostname).
func (r Resolved) LinkURL(l ResolvedLink, w Where) (url, direct string) {
	if !l.OK {
		return "", ""
	}
	if l.OnEntry {
		direct = r.direct(w, l.Service, l.Port, l.HTTPS, l.Path, r.Source == SourceHostRoute)
		if w.ProxyPort > 0 {
			return r.proxied(w, l.HTTPS, l.Path), direct
		}
		return direct, ""
	}
	return r.direct(w, l.Service, l.Port, l.HTTPS, l.Path, false), ""
}
