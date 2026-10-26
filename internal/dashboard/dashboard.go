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
	"github.com/pedropinheiro75/stackfork/internal/metrics"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/prune"
)

//go:embed static
var staticFS embed.FS

//go:embed templates/index.html.tmpl templates/instances.html.tmpl templates/cleanup.html.tmpl
var indexTemplateSrc embed.FS

// indexFuncs is the small set of view helpers the template needs beyond
// plain field access - just enough for the stack icon's initial letter
// (design.md D3a); everything else (escaping, formatting) is either
// html/template's own job or already done in Go before Execute.
var indexFuncs = template.FuncMap{
	"stateChip":       stateChip,
	"serviceChip":     serviceChip,
	"knowledgeTone":   knowledgeTone,
	"displayName":     displayName,
	"lane":            lane,
	"plural":          plural,
	"runnable":        runnable,
	"openApp":         openApp,
	"warningCommands": warningCommands,
	"endpointNote":    endpointNote,
	"isLive":          func(state string) bool { return isRunningState(state) },
	"initial":         initials,
	"appName":         appName,
	"domID":           domID,
	"hostHint":        hostHint,
	"repoRoot":        repoRoot,
}

var indexTemplate = parseIndexTemplate()

// parseIndexTemplate parses the page and the project/version cards. The
// cards' source is written one element per line; app.js renders the same
// markup with no whitespace between tags, so the line breaks and indents are
// dropped here and first paint equals the first poll byte for byte.
func parseIndexTemplate() *template.Template {
	t := template.New("index.html.tmpl").Funcs(indexFuncs)
	page, err := indexTemplateSrc.ReadFile("templates/index.html.tmpl")
	if err != nil {
		panic(err)
	}
	cards, err := indexTemplateSrc.ReadFile("templates/instances.html.tmpl")
	if err != nil {
		panic(err)
	}
	note, err := indexTemplateSrc.ReadFile("templates/cleanup.html.tmpl")
	if err != nil {
		panic(err)
	}
	template.Must(t.Parse(string(page)))
	template.Must(t.New("cleanup.html.tmpl").Parse(string(note)))
	template.Must(t.New("instances.html.tmpl").Parse(leadingBreaks.ReplaceAllString(string(cards), "")))
	return t
}

var leadingBreaks = regexp.MustCompile(`\n[ \t]*`)

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
	// ResetEnv and ResetPreview back "Reset environment...": the preview is the
	// text of the native confirmation (it names the volumes) and whether the
	// checkout cannot be updated; the CLI supplies both.
	ResetEnv     ResetFunc
	ResetPreview ResetPreviewFunc
	// DerivePlan backs the plan list of a repo with no recorded plan steps: the
	// steps a Start would run, with their sources. The CLI supplies it
	// (cli.DerivePlan); nil shows no list for such a repo.
	DerivePlan DeriveFunc
	// RescanAfter is how old the repo scan may get before a request refreshes
	// it; 0 means 5 seconds. Tests raise it so the clock cannot drop caches.
	RescanAfter time.Duration
	// LiveCleanup backs the cleanup ops of POST /api/live; nil answers 503.
	LiveCleanup *LiveCleanup
	// Live backs the Live switch and the live state on the page; nil hides it.
	Live *LiveOps

	mux      http.Handler
	cache    *inventoryCache
	disk     diskState
	repos    reposState
	configMu sync.Mutex
	secrets  secretsState

	// actionMu serializes actions per project (design.md D4: "one at a
	// time per instance"); a project already running an action gets 409.
	actionMu   sync.Mutex
	actionsRun map[string]bool

	// logSlots caps concurrent log streams at 10 (task 4.3, design.md
	// Risks: "max 10 concurrent log streams"); a buffered channel used as
	// a counting semaphore.
	logSlots chan struct{}

	// StartSampler runs the metrics sampler until stop closes; nil (the
	// default, and in tests) never starts it.
	StartSampler func(m *metrics.Sampler, stop <-chan struct{})
	sm           *metrics.Sampler
	smOnce       sync.Once
	// done ends the metrics sampler; Close closes it.
	done     chan struct{}
	doneOnce sync.Once
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
		logSlots: make(chan struct{}, maxLogStreams), done: make(chan struct{}),
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
	s.sampler() // history starts with the dashboard, not with the first Project page view
}

// Close stops the background metrics sampler. Safe to call twice.
func (s *Server) Close() { s.doneOnce.Do(func() { close(s.done) }) }

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
	mux.HandleFunc("GET /api/project", s.handleProject)
	mux.HandleFunc("GET /api/project/resources", s.handleProjectResources)
	mux.HandleFunc("GET /fragments/repos", s.handleReposFragment)
	mux.HandleFunc("GET /fragments/settings", s.handleSettingsFragment)
	mux.HandleFunc("POST /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/cleanup", s.handleCleanup)
	mux.HandleFunc("GET /api/disk", s.handleDisk)
	mux.HandleFunc("POST /api/run", s.handleRun)
	mux.HandleFunc("GET /api/agents", s.handleAgents)
	mux.HandleFunc("POST /api/learn", s.handleLearn)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/secrets", s.handleSecrets)
	mux.HandleFunc("POST /api/secrets/check", s.handleSecretsCheck)
	mux.HandleFunc("POST /api/secrets/set", s.handleSecretsSet)
	mux.HandleFunc("POST /api/secrets/map", s.handleSecretsMap)
	mux.HandleFunc("POST /api/secrets/unmap", s.handleSecretsUnmap)
	mux.HandleFunc("GET /api/env", s.handleEnvGet)
	mux.HandleFunc("POST /api/env", s.handleEnvPost)
	mux.HandleFunc("GET /api/runs", s.handleRuns)
	mux.HandleFunc("GET /api/runs/log", s.handleRunLog)
	mux.HandleFunc("POST /api/actions", s.handleActions)
	mux.HandleFunc("POST /api/live", s.handleLive)
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
	inv, err := s.cache.get(inventory.Options{
		NewStateBase: s.NewStateBase, LegacyStateBase: s.LegacyStateBase, Version: s.Version,
	})
	inv.Instances = s.decorateCleanup(inv.Instances)
	return inv, err
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
	seen := map[string]bool{cmds[0]: true} // a warning repeating `down` adds nothing (app.js commandsOf)
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

// appName is the app an instance belongs to: the main checkout folder
// computed by inventory, else the project's display name.
func appName(inst inventory.Instance) string {
	if inst.App != "" {
		return inst.App
	}
	return displayName(inst.Project)
}

// initials is the avatar text of an app name: the first letter of its first
// two words (split on - _ . space and camelCase), or its first two letters
// when it is one word. app.js's initials must match.
func initials(name string) string {
	var words [][]rune
	var cur []rune
	var prev rune
	for _, r := range name {
		switch {
		case r == '-' || r == '_' || r == '.' || unicode.IsSpace(r):
			if cur != nil {
				words, cur = append(words, cur), nil
			}
		case unicode.IsUpper(r) && unicode.IsLower(prev) && cur != nil:
			words, cur = append(words, cur), []rune{r}
		default:
			cur = append(cur, r)
		}
		prev = r
	}
	if cur != nil {
		words = append(words, cur)
	}
	var out []rune
	switch {
	case len(words) == 0:
		return "?"
	case len(words) == 1:
		out = words[0]
		if len(out) > 2 {
			out = out[:2]
		}
	default:
		out = []rune{words[0][0], words[1][0]}
	}
	return strings.ToUpper(string(out))
}

// stackColorIndex hashes an app name (a simple byte sum, deliberately not a
// real hash function: it only needs to be cheap and identical on both
// sides, not collision-resistant) into 0..stackPaletteSize-1.
func stackColorIndex(app string) int {
	sum := 0
	for i := 0; i < len(app); i++ {
		sum += int(app[i])
	}
	return sum % stackPaletteSize
}

// isRunningState reports whether an instance counts as "running" for the
// D3a Running/Stopped split: any service running counts, same as the
// header's own running count.
func isRunningState(state string) bool {
	return state == inventory.StateRunning || state == inventory.StatePartial
}

// projectData is one app with every version (instance) of it: a ProjectCard
// and its VersionCards. app.js's groupProjects must sort the same way.
type projectData struct {
	App        string
	Root       string // the repo's main checkout, for the project menu
	ColorIndex int
	Versions   []cardData
	Chips      []chipView
}

// repoRoot is the main checkout a version belongs to: the part of its root
// before "/.worktrees/", else the root itself. app.js's repoRoot must match.
func repoRoot(root string) string {
	if i := strings.LastIndex(root, "/.worktrees/"); i > 0 {
		return root[:i]
	}
	return root
}

// domID turns free text into an id fragment (anything outside [A-Za-z0-9_-]
// becomes "-"). app.js's domId must match.
func domID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// hostHint is the "how to run the host app" sentence of a knowledge-only app
// chip; the chip never prints it, only its tooltip and the menu item do.
func hostHint(apps []inventory.AppLink) string {
	for _, a := range apps {
		if a.Source == "knowledge" && a.Hint != "" {
			return a.Hint
		}
	}
	return ""
}

func buildProjects(instances []inventory.Instance) []projectData {
	byApp := map[string]*projectData{}
	var order []*projectData
	for _, inst := range instances {
		app := appName(inst)
		p := byApp[app]
		if p == nil {
			p = &projectData{App: app, Root: repoRoot(inst.Root), ColorIndex: stackColorIndex(app)}
			byApp[app] = p
			order = append(order, p)
		}
		p.Versions = append(p.Versions, cardData{Instance: inst, Commands: cliCommands(inst), ColorIndex: stackColorIndex(app)})
	}
	last := map[*projectData]int64{}
	live := map[*projectData]bool{}
	for _, p := range order {
		running, partial := 0, 0
		for _, v := range p.Versions {
			switch v.State {
			case inventory.StateRunning:
				running++
			case inventory.StatePartial:
				partial++
			}
			if ms := v.Used.UnixMilli(); ms > last[p] {
				last[p] = ms
			}
		}
		live[p] = running+partial > 0
		if running > 0 {
			p.Chips = append(p.Chips, chipView{Class: "ok", Glyph: glyphOK, Label: fmt.Sprintf("%d running", running)})
		}
		if partial > 0 {
			p.Chips = append(p.Chips, chipView{Class: "attention", Glyph: glyphPartial, Label: fmt.Sprintf("%d partial", partial)})
		}
		if !live[p] {
			p.Chips = append(p.Chips, chipView{Class: "off", Glyph: glyphOff, Label: "Stopped"})
		}
		sort.SliceStable(p.Versions, func(i, j int) bool {
			a, b := p.Versions[i], p.Versions[j]
			if al, bl := isRunningState(a.State), isRunningState(b.State); al != bl {
				return al
			}
			if a.IsMain != b.IsMain {
				return a.IsMain
			}
			if a.Branch != b.Branch {
				return a.Branch < b.Branch
			}
			return a.Project < b.Project
		})
	}
	// Projects: any live version first, then the newest activity, then name.
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if live[a] != live[b] {
			return live[a]
		}
		if last[a] != last[b] {
			return last[a] > last[b]
		}
		return a.App < b.App
	})
	out := make([]projectData, len(order))
	for i, p := range order {
		out[i] = *p
	}
	return out
}

type pageData struct {
	Version      string
	Token        string
	Docker       inventory.DockerStatus
	Proxy        inventory.ProxyStatus
	Projects     []projectData
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
		running := 0
		for _, inst := range c.inv.Instances {
			if isRunningState(inst.State) {
				running++
			}
		}
		data = pageData{
			Version: s.Version, Token: s.Token, Docker: c.inv.Docker, Proxy: c.inv.Proxy,
			Projects: buildProjects(c.inv.Instances), Total: len(c.inv.Instances), RunningCount: running,
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
