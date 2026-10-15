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
	"strings"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/prune"
)

//go:embed static
var staticFS embed.FS

//go:embed templates/index.html.tmpl
var indexTemplateSrc embed.FS

// indexFuncs is the small set of view helpers the template needs beyond
// plain field access - just enough for the stack icon's initial letter
// (design.md D3a); everything else (escaping, formatting) is either
// html/template's own job or already done in Go before Execute.
var indexFuncs = template.FuncMap{
	"dotClass": dotClass,
	"dotTitle": dotTitle,
	"initial": func(project string) string {
		for _, r := range projectSlug(project) {
			return strings.ToUpper(string(r))
		}
		return "?"
	},
}

var indexTemplate = template.Must(
	template.New("index.html.tmpl").Funcs(indexFuncs).ParseFS(indexTemplateSrc, "templates/index.html.tmpl"))

// Server holds everything a request handler needs; every field is set once
// at construction and read-only afterward except the inventory cache.
type Server struct {
	NewStateBase    string
	LegacyStateBase string
	Version         string
	Port            int // this server's own listening port (Host allowlist)
	Token           string

	// ProfileDirs and StartEnv are set by the CLI after New: profile
	// directories for readiness, and the Start button's code path.
	ProfileDirs []string
	StartEnv    StartFunc

	mux      http.Handler
	cache    *inventoryCache
	disk     diskState
	repos    reposState
	configMu sync.Mutex

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

// Warm fills the inventory cache and the repo scan, so the first page load
// finds them ready. Best effort; callers run it in the background.
func (s *Server) Warm() {
	s.collect() //nolint:errcheck // a failure just leaves the cache cold
	s.currentRepos()
	s.disk.get()
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
	mux.HandleFunc("GET /api/repos", s.handleRepos)
	mux.HandleFunc("GET /fragments/repos", s.handleReposFragment)
	mux.HandleFunc("GET /fragments/settings", s.handleSettingsFragment)
	mux.HandleFunc("POST /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/cleanup", s.handleCleanup)
	mux.HandleFunc("GET /api/disk", s.handleDisk)
	mux.HandleFunc("POST /api/run", s.handleRun)
	mux.HandleFunc("GET /api/agents", s.handleAgents)
	mux.HandleFunc("POST /api/learn", s.handleLearn)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("POST /api/actions", s.handleActions)
	mux.HandleFunc("POST /api/copy", s.handleCopy)
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

// handleCleanup is the read-only dry-run summary behind the header's "Clean
// up" panel: how many instances each `stackfork prune` scope would remove.
// Nothing is deleted here or anywhere in the dashboard; the page only shows
// the commands to copy.
func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	inv, err := s.collect()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	l, err := prune.List(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	now := time.Now()
	plan := prune.BuildPlan(inv, s.NewStateBase, s.LegacyStateBase, l, now)
	type group struct {
		Count   int    `json:"count"`
		Command string `json:"command"`
	}
	mk := func(sc prune.Scopes, flag string) group {
		sel := prune.Select(plan.Candidates, sc, 0, now)
		return group{Count: len(sel), Command: "stackfork prune " + flag}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]group{
		"stopped":  mk(prune.Scopes{Stopped: true}, "--stopped"),
		"orphaned": mk(prune.Scopes{Orphaned: true}, "--orphaned"),
		"all":      mk(prune.Scopes{All: true}, "--all"),
	})
}

// cardData is one instance plus the render-only extras the template needs
// (task 4.4): the exact CLI commands for the operations the dashboard never
// offers a button for (design.md D4's "destructive or input-heavy commands
// stay in the CLI; the page shows the exact command to copy").
type cardData struct {
	inventory.Instance
	Commands   []string
	ColorIndex int
}

// cliCommands lists the copyable commands for one card: `down --volumes`
// always (a card never gets a button for it), plus whatever a warning's own
// text already names (gc for an orphaned/branchless instance,
// --reassign-ports for a busy port) - read back off the warning strings
// rather than re-deriving the same conditions inventory.Collect already
// computed, so the two can't drift apart.
func cliCommands(inst inventory.Instance) []string {
	cmds := []string{fmt.Sprintf("stackfork down --instance %s --volumes", inst.Project)}
	seen := map[string]bool{}
	for _, w := range inst.Warnings {
		start := strings.IndexByte(w, '`')
		if start < 0 {
			continue
		}
		end := strings.IndexByte(w[start+1:], '`')
		if end < 0 {
			continue
		}
		cmd := w[start+1 : start+1+end]
		if !seen[cmd] {
			seen[cmd] = true
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// stackPalette is the fixed set of stack-icon colors (design.md D3a: "a
// stack icon colored from a hash of the project name"), applied as a CSS
// class (app.css's .stack-color-0..7) rather than an inline color, so the
// same small integer hash - computed identically in Go and in app.js's
// re-render - is the only thing that has to match between them.
const stackPaletteSize = 8

// projectSlug strips the sf-/idv- instance prefix off a project name, so the
// stack icon's initial letter and color are taken from the meaningful part
// of the name instead of always reading the same prefix (every legacy
// instance otherwise shows "R", maintainer feedback: app-b, not the prefix, should
// give "C").
func projectSlug(project string) string {
	for _, p := range []string{identity.Prefix + "-", identity.LegacyPrefix + "-"} {
		if strings.HasPrefix(project, p) {
			return project[len(p):]
		}
	}
	return project
}

// stackColorIndex hashes a project's slug (a simple byte sum, deliberately
// not a real hash function: it only needs to be cheap and identical on both
// sides, not collision-resistant) into 0..stackPaletteSize-1.
func stackColorIndex(project string) int {
	slug := projectSlug(project)
	sum := 0
	for i := 0; i < len(slug); i++ {
		sum += int(slug[i])
	}
	return sum % stackPaletteSize
}

// isRunningState reports whether an instance counts as "running" for the
// D3a Running/Stopped split: any service running counts, same as the
// header's own running count.
func isRunningState(state string) bool {
	return state == inventory.StateRunning || state == inventory.StatePartial
}

func buildCards(instances []inventory.Instance) (running, stopped []cardData) {
	for _, inst := range instances {
		card := cardData{Instance: inst, Commands: cliCommands(inst), ColorIndex: stackColorIndex(inst.Project)}
		if isRunningState(inst.State) {
			running = append(running, card)
		} else {
			stopped = append(stopped, card)
		}
	}
	less := func(cards []cardData) func(i, j int) bool {
		return func(i, j int) bool {
			if cards[i].Root != cards[j].Root {
				return cards[i].Root < cards[j].Root
			}
			return cards[i].Project < cards[j].Project
		}
	}
	sort.Slice(running, less(running))
	sort.Slice(stopped, less(stopped))
	return running, stopped
}

type pageData struct {
	Version      string
	Token        string
	Docker       inventory.DockerStatus
	Proxy        inventory.ProxyStatus
	Running      []cardData
	Stopped      []cardData
	Total        int
	RunningCount int
	// Loading: the first inventory collection is still running; the page
	// renders at once and app.js fills it in from /api/instances.
	Loading bool
}

// IndexWait is how long the page waits for a cold inventory before it
// renders in its "loading" state instead (a var so tests can wait longer).
var IndexWait = 250 * time.Millisecond

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	type collected struct {
		inv inventory.Inventory
		err error
	}
	ch := make(chan collected, 1)
	go func() {
		inv, err := s.collect()
		ch <- collected{inv, err}
	}()
	var data pageData
	select {
	case c := <-ch:
		if c.err != nil {
			http.Error(w, c.err.Error(), http.StatusInternalServerError)
			return
		}
		running, stopped := buildCards(c.inv.Instances)
		data = pageData{
			Version: s.Version, Token: s.Token, Docker: c.inv.Docker, Proxy: c.inv.Proxy,
			Running: running, Stopped: stopped, Total: len(c.inv.Instances), RunningCount: len(running),
		}
	case <-time.After(IndexWait):
		// Docker is slow: show the shell now (the collection keeps running
		// and warms the cache for the poll that app.js makes at once).
		data = pageData{Version: s.Version, Token: s.Token, Docker: inventory.DockerStatus{Reachable: true}, Loading: true}
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
	mode, err := proxy.Mode()
	if err != nil {
		return err
	}
	proxyPort, err := proxy.ReadPort(newStateBase)
	if err != nil {
		return err
	}
	all := append(append([]proxy.Route{}, routes...), proxy.Route{
		Hostname: ProxyHostname, Target: proxy.HostTarget(mode, port),
	})
	return proxy.WithLock(newStateBase, func() error {
		if err := proxy.WriteCaddyfile(newStateBase, all, mode, proxyPort); err != nil {
			return err
		}
		if err := proxy.Reload(newStateBase); err != nil {
			// The file was already written with the dashboard's route; a
			// failed reload must not leave it there for the next caller to
			// find (and never clean up, since cmdDashboard only unregisters
			// when this call reported success).
			proxy.WriteCaddyfile(newStateBase, routes, mode, proxyPort) //nolint:errcheck // best-effort rollback; the reload error is what's reported
			return err
		}
		return nil
	})
}

// UnregisterFromProxy removes the dashboard's route on shutdown, keeping
// every instance route intact.
func UnregisterFromProxy(newStateBase string, routes []proxy.Route) error {
	mode, err := proxy.Mode()
	if err != nil {
		return err
	}
	proxyPort, err := proxy.ReadPort(newStateBase)
	if err != nil {
		return err
	}
	return proxy.WithLock(newStateBase, func() error {
		if err := proxy.WriteCaddyfile(newStateBase, routes, mode, proxyPort); err != nil {
			return err
		}
		return proxy.Reload(newStateBase)
	})
}
