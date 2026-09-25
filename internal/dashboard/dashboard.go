// Package dashboard serves stackfork's local web page: a read-mostly view
// of every instance (internal/inventory) plus the safe actions from
// design.md D4, behind the D5 security boundary. It runs in the foreground
// for the lifetime of `stackfork dashboard`.
package dashboard

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
)

//go:embed static
var staticFS embed.FS

//go:embed templates/index.html.tmpl
var indexTemplateSrc embed.FS

var indexTemplate = template.Must(template.ParseFS(indexTemplateSrc, "templates/index.html.tmpl"))

// Server holds everything a request handler needs; every field is set once
// at construction and read-only afterward except the inventory cache.
type Server struct {
	NewStateBase    string
	LegacyStateBase string
	Version         string
	Port            int // this server's own listening port (Host allowlist)
	Token           string

	mux   http.Handler
	cache *inventoryCache

	// actionMu serializes actions per project (design.md D4: "one at a
	// time per instance"); a project already running an action gets 409.
	actionMu   sync.Mutex
	actionsRun map[string]bool

	// logSlots caps concurrent log streams at 10 (task 4.3, design.md
	// Risks: "max 10 concurrent log streams"); a buffered channel used as
	// a counting semaphore.
	logSlots chan struct{}
}

// maxLogStreams is design.md's documented cap.
const maxLogStreams = 10

// New builds a Server with a fresh per-process token (design.md D5).
func New(newStateBase, legacyStateBase, version string, port int) (*Server, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	s := &Server{
		NewStateBase: newStateBase, LegacyStateBase: legacyStateBase,
		Version: version, Port: port, Token: token,
		cache: &inventoryCache{}, actionsRun: map[string]bool{},
		logSlots: make(chan struct{}, maxLogStreams),
	}
	s.mux = s.routes()
	return s, nil
}

// Handler returns the full stack: security headers and the Host allowlist
// wrap every route (design.md D5).
func (s *Server) Handler() http.Handler {
	return s.withSecurity(s.mux)
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/instances", s.handleInstances)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("POST /api/actions", s.handleActions)
	mux.HandleFunc("GET /static/", s.handleStatic)
	return mux
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.StripPrefix("/static/", http.FileServer(http.FS(sub))).ServeHTTP(w, r)
}

// collect returns the cached inventory (2 s TTL, one collection in flight
// at a time - design.md D2), scoped to this server's state directories.
func (s *Server) collect() (inventory.Inventory, error) {
	return s.cache.get(inventory.Options{
		NewStateBase: s.NewStateBase, LegacyStateBase: s.LegacyStateBase, Version: s.Version,
	})
}

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	inv, err := s.collect()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(inv); err != nil {
		// Headers/body may already be partially written; nothing more to
		// do but note it happened (there is no client left to answer).
		return
	}
}

// checkoutGroup is one checkout's instances, for the page's "grouped by
// checkout" layout (design.md D3).
type checkoutGroup struct {
	Root      string
	Instances []inventory.Instance
}

func groupByCheckout(instances []inventory.Instance) []checkoutGroup {
	var groups []checkoutGroup
	byRoot := map[string]int{}
	for _, inst := range instances {
		if idx, ok := byRoot[inst.Root]; ok {
			groups[idx].Instances = append(groups[idx].Instances, inst)
			continue
		}
		byRoot[inst.Root] = len(groups)
		groups = append(groups, checkoutGroup{Root: inst.Root, Instances: []inventory.Instance{inst}})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Root < groups[j].Root })
	return groups
}

type pageData struct {
	Version      string
	Token        string
	Docker       inventory.DockerStatus
	Proxy        inventory.ProxyStatus
	Groups       []checkoutGroup
	Total        int
	RunningCount int
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	inv, err := s.collect()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	running := 0
	for _, i := range inv.Instances {
		if i.State == inventory.StateRunning || i.State == inventory.StatePartial {
			running++
		}
	}
	data := pageData{
		Version: s.Version, Token: s.Token, Docker: inv.Docker, Proxy: inv.Proxy,
		Groups: groupByCheckout(inv.Instances), Total: len(inv.Instances), RunningCount: running,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- inventory cache -------------------------------------------------------

// cacheTTL is design.md D2's "cached for 2 s and shared by concurrent
// requests".
const cacheTTL = 2 * time.Second

// inventoryCache is a minimal TTL cache with single-flight collapsing: at
// most one inventory.Collect runs at a time, and every caller within the
// TTL window gets its result, so many dashboard tabs polling at once never
// multiply `docker ps` calls (design.md D2).
type inventoryCache struct {
	mu      sync.Mutex
	last    inventory.Inventory
	lastErr error
	lastAt  time.Time
	pending chan struct{}
}

func (c *inventoryCache) get(opts inventory.Options) (inventory.Inventory, error) {
	c.mu.Lock()
	if c.pending == nil && time.Since(c.lastAt) < cacheTTL {
		inv, err := c.last, c.lastErr
		c.mu.Unlock()
		return inv, err
	}
	if c.pending != nil {
		ch := c.pending
		c.mu.Unlock()
		<-ch
		c.mu.Lock()
		inv, err := c.last, c.lastErr
		c.mu.Unlock()
		return inv, err
	}
	ch := make(chan struct{})
	c.pending = ch
	c.mu.Unlock()

	inv, err := inventory.Collect(opts)

	c.mu.Lock()
	c.last, c.lastErr, c.lastAt = inv, err, time.Now()
	c.pending = nil
	c.mu.Unlock()
	close(ch)
	return inv, err
}

// --- proxy route registration ----------------------------------------------

// ProxyHostname is the dashboard's own route while it runs (design.md D2:
// "registers stackfork.localhost with the proxy").
const ProxyHostname = "stackfork.localhost"

// RegisterWithProxy adds the dashboard's route to the shared proxy's
// Caddyfile alongside every instance route, and reloads it if the proxy is
// running. routes is every other route (from the CLI's own route-gathering)
// so this never drops instance routing.
func RegisterWithProxy(newStateBase string, routes []proxy.Route, port int) error {
	all := append(append([]proxy.Route{}, routes...), proxy.Route{
		Hostname: ProxyHostname, Target: fmt.Sprintf("host.docker.internal:%d", port),
	})
	if err := proxy.WriteCaddyfile(newStateBase, all); err != nil {
		return err
	}
	if err := proxy.Reload(newStateBase); err != nil {
		// The file was already written with the dashboard's route; a
		// failed reload must not leave it there for the next caller to
		// find (and never clean up, since cmdDashboard only unregisters
		// when this call reported success).
		proxy.WriteCaddyfile(newStateBase, routes) //nolint:errcheck // best-effort rollback; the reload error is what's reported
		return err
	}
	return nil
}

// UnregisterFromProxy removes the dashboard's route on shutdown, keeping
// every instance route intact.
func UnregisterFromProxy(newStateBase string, routes []proxy.Route) error {
	if err := proxy.WriteCaddyfile(newStateBase, routes); err != nil {
		return err
	}
	return proxy.Reload(newStateBase)
}
