// Package inventory builds the single view of every instance on the
// machine (design.md D1): `stackfork list`, `list --json` and the
// dashboard's GET /api/instances all render the same Inventory, so they
// cannot disagree.
package inventory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Instance states (design.md D1).
const (
	StateRunning         = "running"
	StatePartial         = "partial"
	StateStopped         = "stopped"
	StateMissingCheckout = "missing-checkout"
	StateMissingBranch   = "missing-branch"
	StateUnknown         = "unknown"
	// StateNotStarted is a service whose container exists but never ran
	// (docker's "created"): red, and it counts toward partial.
	StateNotStarted = "not-started"
	// StateWaiting is a service that was never started only because what it
	// depends on is still starting (Compose starts it by itself afterwards):
	// amber, with WaitingFor naming the dependencies.
	StateWaiting = "waiting"

	// ServiceOptional is a service that never started: it has no container
	// at all and is either behind a Compose profile or not part of the
	// default `up`. It is not "stopped", and never makes an instance partial.
	ServiceOptional = "optional"
)

// DockerStatus reports whether the daemon answered `docker ps`.
type DockerStatus struct {
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// ProxyStatus mirrors proxy.Info, exposed here so a dashboard/list consumer
// needs only the inventory package.
type ProxyStatus struct {
	Running bool `json:"running"`
	Port    int  `json:"port"`
	// Hint is set when routes exist but the proxy is down.
	Hint string `json:"hint,omitempty"`
}

// Endpoint is one published port, ready to render: a link for http/https, a
// copyable address otherwise. Every declared port in the snapshot gets one,
// whether or not the service is currently running (maintainer feedback on
// task 3.6: a stopped/exited service must not go address-less). Known is
// false only for a legacy/--random-ports instance's port that isn't
// currently running - Docker picks that port at container-create time, so
// there is nothing to show but the container-side port until it starts.
type Endpoint struct {
	Scheme    string `json:"scheme"`
	URL       string `json:"url"`
	Container int    `json:"container"`
	Known     bool   `json:"known"`
	// Proxied marks the extra, portless URL added when the shared proxy is
	// actually routing this service (design.md D7 layer 3) - shown
	// alongside, not instead of, the direct address.
	Proxied bool `json:"proxied,omitempty"`
}

// PortMapping is one container->host port mapping declared in the
// snapshot, for a service's expandable settings (design.md D3a).
type PortMapping struct {
	Container int    `json:"container"`
	Host      int    `json:"host,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
}

// VolumeMount is one volume or bind mount declared in the snapshot.
type VolumeMount struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Service is one Compose service's current row within an instance.
type Service struct {
	Name string `json:"name"`
	// Label is the text shown instead of Name when it differs (a host
	// process row has the id "host:NAME" and the label NAME).
	Label  string `json:"label,omitempty"`
	State  string `json:"state"`
	Health string `json:"health,omitempty"`
	// HealthDetail is the last health-check line of an unhealthy service.
	HealthDetail string `json:"health_detail,omitempty"`
	// WaitingFor lists the dependencies a StateWaiting service waits for.
	WaitingFor []string   `json:"waiting_for,omitempty"`
	Endpoints  []Endpoint `json:"endpoints,omitempty"`

	// The rest are read from the snapshot already on disk (design.md D3a):
	// no extra docker call. Image and Kind pick the dashboard's logo;
	// EnvKeys is names only, never a value.
	Image       string        `json:"image,omitempty"`
	Kind        string        `json:"kind,omitempty"`
	Ports       []PortMapping `json:"ports,omitempty"`
	Volumes     []VolumeMount `json:"volumes,omitempty"`
	Healthcheck string        `json:"healthcheck,omitempty"`
	DependsOn   []string      `json:"depends_on,omitempty"`
	Replicas    int           `json:"replicas,omitempty"`
	EnvKeys     []string      `json:"env_keys,omitempty"`
	// ConnectionURL is a copy-paste connection string for a known database
	// kind (task 3.7, design.md D3a): built from non-secret identifiers
	// (user, database name) read off an explicit allowlist of env key
	// names, with a literal "<password>" placeholder standing in for
	// whatever secret actually guards it - never a real value, and never
	// even reads a password-shaped key. Empty when the kind isn't a known
	// database or its host port isn't known yet.
	ConnectionURL string `json:"connection_url,omitempty"`
	// UseCommands are the extra copy-ready client commands/values D3b §3
	// wants (a psql/mysql/redis-cli invocation, a stackfork exec form, or a
	// mail catcher's SMTP_HOST/SMTP_PORT) - same placeholder-safety
	// guarantee as ConnectionURL.
	UseCommands []UseCommand `json:"use_commands,omitempty"`

	// knownHTTPPorts are well-known web UI ports for specific images (fix
	// 3: a mail catcher's UI port, e.g. Mailpit's 8025, renders as a link;
	// its SMTP port stays tcp). Unexported: internal to endpoint scheme
	// resolution, never serialized, never a guess for an unrecognized image.
	knownHTTPPorts []int
}

// KnowledgeStatus is an instance's learned-knowledge summary (design.md
// D10): none (nil), draft, verified (with date) or stale (with the files
// that changed since the scan that was verified).
type KnowledgeStatus struct {
	Status       string   `json:"status"`
	Date         string   `json:"date,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

// Instance is one prepared checkout+branch.
type Instance struct {
	Project  string `json:"project"`
	Root     string `json:"root"`
	Branch   string `json:"branch"`
	Hostname string `json:"hostname"`
	// RelRoot is Root as the dashboard shows it: ".worktrees/<name>" for a
	// linked worktree, "main checkout" for a repo's own checkout, else the
	// path with the home directory written "~".
	RelRoot string `json:"rel_root,omitempty"`
	// App is the folder name of the repo's main checkout (for a worktree,
	// the main repo it belongs to), empty when Root is not a git checkout.
	App string `json:"app,omitempty"`
	// IsMain marks a repo's own checkout (RelRoot "main checkout").
	IsMain bool   `json:"is_main,omitempty"`
	Legacy bool   `json:"legacy"`
	State  string `json:"state"`

	Services  []Service        `json:"services,omitempty"`
	Knowledge *KnowledgeStatus `json:"knowledge,omitempty"`
	Warnings  []string         `json:"warnings,omitempty"`
	// Files is the instance's source Compose files, part of each service's
	// expandable settings (design.md D3a/task 3.4).
	Files []string `json:"files,omitempty"`
	// Apps is every way to open or reach this stack's app(s) (design.md
	// D3b, task 3.8): from a service's own HTTP endpoint, a host-run
	// `route add` registration, or - only when nothing else qualifies - a
	// learned dev script's name as a hint. The goal of the page is to open
	// and use the app, not inspect containers.
	Apps []AppLink `json:"apps,omitempty"`
	// EntryNone is knowledge's entry {none:true}: there is no web app, so
	// nothing guesses one for Open App (entry-links D6).
	EntryNone bool `json:"entry_none,omitempty"`
	// EnvBlock is a copy-paste ".env" fragment for a host-run app to talk
	// to this stack's services (task 3.10, design.md D3b §4): one
	// DATABASE_URL/REDIS_URL/MONGODB_URL/SMTP_HOST/SMTP_PORT line per
	// qualifying service, in the same placeholder-safe form as
	// Service.ConnectionURL. Empty when nothing in the stack qualifies.
	EnvBlock string `json:"env_block,omitempty"`
	// SeedCommand/SeedForce are set only while a seed failed (seeded:false in
	// the manifest and a seed to run): the dashboard shows a warning with the
	// copyable retry commands. A successful or absent seed shows nothing.
	SeedCommand string `json:"seed_command,omitempty"`
	// OneOffRunning counts `compose run` containers still running;
	// OneOffLeftover those created or exited and never removed, with the
	// `docker rm` that clears them.
	OneOffRunning  int    `json:"oneoff_running,omitempty"`
	OneOffLeftover int    `json:"oneoff_leftover,omitempty"`
	OneOffCleanup  string `json:"oneoff_cleanup,omitempty"`
	SeedForce      string `json:"seed_force,omitempty"`
	// SeedDone records the seed as done without running it; SeedError is
	// the end of the failed run's output.
	SeedDone  string `json:"seed_done,omitempty"`
	SeedError string `json:"seed_error,omitempty"`
	// SeedRun and SeedStep name the run step whose log SeedError is the end
	// of; the card's View log opens it. Zero when SeedError came from seed.log.
	SeedRun  int    `json:"seed_run,omitempty"`
	SeedStep string `json:"seed_step,omitempty"`
	// LastRun is the newest run of the instance (design.md D14); nil when it has none.
	LastRun *LastRun `json:"last_run,omitempty"`
	// Cleanup is the branch-cleanup callout of this instance's worktree
	// (add-live-mode D20); the dashboard fills it from the live loop.
	Cleanup *CleanupNote `json:"cleanup,omitempty"`

	// Recipe inputs recorded at prepare, kept out of the JSON: a sibling
	// worktree's recipe is derived from them (add-repo-browser D6a).
	EnvFile string            `json:"-"`
	Secrets map[string]string `json:"-"` // references only, never values
	// Used is the manifest mtime: the last activity the dashboard sorts by.
	Used time.Time `json:"used"`
	// SeedOnce/SeedService are the manifest's recorded seed recipe.
	SeedOnce    string `json:"-"`
	SeedService string `json:"-"`
}

// Inventory is the whole machine's state, task 1.1's D1 type.
type Inventory struct {
	Version   string       `json:"version"`
	Docker    DockerStatus `json:"docker"`
	Proxy     ProxyStatus  `json:"proxy"`
	Instances []Instance   `json:"instances"`
}

// Options selects the state directories and version to report; every field
// is required (callers pass the same values CLI actions already resolve).
type Options struct {
	NewStateBase    string
	LegacyStateBase string
	Version         string
}

// Collect builds the Inventory (task 1.1): both state directories, one
// `docker ps --all` call across both label namespaces, git checks for a
// missing checkout or branch, and each instance's knowledge status. A
// manifest that fails to parse becomes a StateUnknown instance with a
// warning rather than failing the whole collection (design.md D6); Docker
// being unreachable is reported in Docker, not returned as an error.
func Collect(opts Options) (Inventory, error) {
	inv := Inventory{Version: opts.Version}

	proxyInfo, err := proxy.Status(opts.NewStateBase)
	if err != nil {
		return Inventory{}, err
	}
	inv.Proxy = ProxyStatus{Running: proxyInfo.Running, Port: proxyInfo.Port}

	containers, dockerErr := listContainers()
	if dockerErr != nil {
		inv.Docker = DockerStatus{Reachable: false, Error: dockerErr.Error()}
	} else {
		inv.Docker = DockerStatus{Reachable: true}
		inv.Proxy.Hint = proxy.DownHint(opts.NewStateBase, inv.Proxy.Running)
	}
	byProject := map[string][]containerRow{}
	for _, c := range containers {
		byProject[c.Project] = append(byProject[c.Project], c)
	}

	type entry struct {
		base, prefix, path string
		legacy             bool
	}
	var entries []entry
	for _, e := range []struct {
		base, prefix string
		legacy       bool
	}{
		{opts.NewStateBase, identity.Prefix, false},
		{opts.LegacyStateBase, identity.LegacyPrefix, true},
	} {
		matches, err := state.Manifests(e.base, e.prefix)
		if err != nil {
			return Inventory{}, err
		}
		for _, m := range matches {
			entries = append(entries, entry{base: e.base, prefix: e.prefix, path: m, legacy: e.legacy})
		}
	}

	for _, e := range entries {
		inst := buildInstance(e.path, e.legacy, byProject[filepath.Base(filepath.Dir(e.path))], inv.Proxy)
		inv.Instances = append(inv.Instances, inst)
	}

	sort.Slice(inv.Instances, func(i, j int) bool {
		a, b := inv.Instances[i], inv.Instances[j]
		if a.Root != b.Root {
			return a.Root < b.Root
		}
		if a.Branch != b.Branch {
			return a.Branch < b.Branch
		}
		return a.Project < b.Project
	})

	return inv, nil
}

// buildInstance turns one manifest into an Instance, given the containers
// (if any) already parsed for its Compose project.
func buildInstance(manifestPath string, legacy bool, rows []containerRow, proxyStatus ProxyStatus) Instance {
	project := filepath.Base(filepath.Dir(manifestPath))
	info, err := state.ReadManifest(manifestPath)
	if err != nil || info == nil {
		return Instance{
			Project: project, Legacy: legacy, State: StateUnknown,
			Warnings: []string{fmt.Sprintf("manifest could not be read (%v); inspect %s manually", err, manifestPath)},
		}
	}

	hostname, _ := identity.BrowserHostname(info.Project, info.Hostname)
	inst := Instance{
		Project: info.Project, Root: info.Root, RelRoot: relRoot(info.Root), App: appName(info.Root), Branch: info.Branch,
		Hostname: hostname, Legacy: legacy, Files: info.Files,
		Secrets: info.ResolvedSecrets(),
	}
	if info.EnvFile != nil {
		inst.EnvFile = *info.EnvFile
	}
	inst.IsMain = inst.RelRoot == "main checkout"
	inst.SeedOnce, inst.SeedService = info.SeedOnce, info.SeedService
	fillSeed(&inst, info, filepath.Dir(manifestPath))
	inst.LastRun = lastRun(filepath.Dir(manifestPath))
	if fi, err := os.Stat(manifestPath); err == nil {
		inst.Used = fi.ModTime()
	}

	statusRows := toStatusRows(rows)

	switch {
	case !checkoutExists(info.Root):
		inst.State = StateMissingCheckout
		inst.Warnings = append(inst.Warnings, fmt.Sprintf("checkout removed at %s; run `stackfork gc`", info.Root))
	case !branchExists(info.Root, info.Branch):
		inst.State = StateMissingBranch
		inst.Warnings = append(inst.Warnings, fmt.Sprintf("branch %s no longer exists in %s; run `stackfork gc`", info.Branch, info.Root))
	default:
		services, serviceState, apps, appWarnings := servicesForInstance(manifestPath, statusRows, hostname, info, proxyStatus)
		inst.Services = append(append([]Service{}, services...), hostServices(info)...)
		inst.State = serviceState
		inst.Warnings = append(inst.Warnings, serviceWarnings(project, services)...)
		inst.Warnings = append(inst.Warnings, appWarnings...)
		fillOneOffs(&inst, rows)
		inst.Apps = appendRouteAndKnowledgeApps(apps, info)
		if k, _ := knowledge.LoadForCheckout(info.Root); k != nil && k.Entry != nil && k.Entry.None {
			inst.EntryNone = true
		}
		inst.EnvBlock = buildEnvBlock(services)
	}

	inst.Warnings = append(inst.Warnings, busyPortWarnings(info, statusRows)...)

	if k, ok := knowledgeStatus(info.Root); ok {
		inst.Knowledge = k
		if k.Status == knowledge.StatusStale {
			inst.Warnings = append(inst.Warnings, fmt.Sprintf(
				"knowledge is stale (changed: %s); run `stackfork learn` to refresh", strings.Join(k.ChangedFiles, ", ")))
		}
	}

	return inst
}

func checkoutExists(root string) bool {
	fi, err := os.Stat(root)
	return err == nil && fi.IsDir()
}

// branchExists mirrors `git show-ref --verify --quiet refs/heads/<branch>`,
// with two allowances for branches that have no refs/heads/ entry yet but
// are not "missing" in any useful sense: a detached-HEAD identity
// (identity's "detached-<sha>" branch value, cli.go currentBranch) never
// lived under refs/heads, and the checkout's own currently checked-out
// branch is never missing even before its first commit (an unborn branch:
// HEAD already points to it symbolically, but git writes the refs/heads/
// entry only at the first commit).
func branchExists(root, branch string) bool {
	if strings.HasPrefix(branch, "detached-") {
		return true
	}
	if out, err := exec.Command("git", "-C", root, "symbolic-ref", "--short", "-q", "HEAD").Output(); err == nil {
		if strings.TrimSpace(string(out)) == branch {
			return true
		}
	}
	err := exec.Command("git", "-C", root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run()
	return err == nil
}

// readComposeServices reads an instance's rendered compose.json and
// returns its "services" map (name -> raw entry), the shape every per-
// service field-reader in this package (fillServiceDetails, connectionURL,
// useCommands, LiteralPassword's caller) expects.
func readComposeServices(instanceDir string) (map[string]interface{}, error) {
	configData, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil, err
	}
	model, err := compose.DecodeJSON(configData)
	if err != nil {
		return nil, err
	}
	m, _ := model.(map[string]interface{})
	svcMap, _ := m["services"].(map[string]interface{})
	return svcMap, nil
}

// ReadComposeServices exposes readComposeServices for the dashboard's
// POST /api/copy (task 3.11): the only other caller of a service's raw
// compose.json entry outside Collect's own pipeline.
func ReadComposeServices(instanceDir string) (map[string]interface{}, error) {
	return readComposeServices(instanceDir)
}

// servicesForInstance reads the instance's snapshot for its declared
// service list (so a never-started service still gets a row) and matches
// each one against its container, if any.
func servicesForInstance(manifestPath string, rows []compose.StatusRow, hostname string, info *state.Manifest, proxyStatus ProxyStatus) ([]Service, string, []AppLink, []string) {
	instanceDir := filepath.Dir(manifestPath)
	svcMap, err := readComposeServices(instanceDir)
	if err != nil {
		return nil, StateStopped, nil, nil
	}
	names := make([]string, 0, len(svcMap))
	for name := range svcMap {
		names = append(names, name)
	}
	sort.Strings(names)

	liveByService := map[string]map[int]compose.Endpoint{}
	for _, e := range compose.ComputeEndpoints(rows, hostname, info.HTTPPorts, info.HTTPSPorts) {
		if t, ok := compose.TargetAsInt(e.Target); ok {
			if liveByService[e.Service] == nil {
				liveByService[e.Service] = map[int]compose.Endpoint{}
			}
			liveByService[e.Service][t] = e
		}
	}
	rowByService := map[string]compose.StatusRow{}
	for _, r := range rows {
		rowByService[r.Service] = r
	}
	model := map[string]interface{}{"services": svcMap}
	primaryService, primaryTarget, primaryHTTPS, hasPrimary := compose.PrimaryHTTPService(model)
	// The recorded entry (or the host route) replaces the guess (entry-links D6).
	k, kerr := knowledge.LoadForCheckout(info.Root)
	entry := knowledge.ResolveEntry(k, model, info.Routes)
	inPlay := k != nil && k.Entry != nil && !k.Entry.Proposed
	if inPlay && entry.Source != knowledge.SourceHostRoute {
		primaryService, primaryTarget, primaryHTTPS, hasPrimary = entry.Service, entry.Port, entry.HTTPS, entry.OK
	}

	var services []Service
	running, total := 0, len(names)
	for _, name := range names {
		svc := Service{Name: name, State: StateStopped}
		raw0, _ := svcMap[name].(map[string]interface{})
		if _, ok := rowByService[name]; !ok {
			if p, _ := raw0["profiles"].([]interface{}); len(p) > 0 || len(rowByService) > 0 {
				svc.State = ServiceOptional // ponytail: no container ever created
				total--
			}
		}
		if row, ok := rowByService[name]; ok {
			svc.State = strings.ToLower(row.State)
			if svc.State == "created" {
				svc.State = StateNotStarted
			}
			svc.HealthDetail = row.HealthDetail
			svc.Health = row.Health
			if svc.State == "running" {
				running++
			}
		}
		raw, hasRaw := svcMap[name].(map[string]interface{})
		if hasRaw {
			fillServiceDetails(&svc, raw)
		}
		// Every declared port gets an endpoint, running or not (maintainer
		// feedback, task 3.6): live docker data wins when there is any (the
		// only way to know a legacy/--random-ports instance's actual bound
		// port), the snapshot's own recorded port otherwise, and "not yet
		// known" only for a random-port service that isn't running to show
		// it against.
		svc.Endpoints = buildServiceEndpoints(svc, liveByService[name], hostname, info)
		if proxyStatus.Running && hasPrimary && name == primaryService && svc.State == "running" {
			svc.Endpoints = append(svc.Endpoints, proxyEndpoint(hostname, primaryHTTPS, proxyStatus.Port, primaryTarget))
		}
		if hasRaw {
			svc.ConnectionURL = connectionURL(svc, raw)
			svc.UseCommands = useCommands(svc, raw)
		}
		services = append(services, svc)
	}

	markWaiting(services)
	where := knowledge.Where{Hostname: hostname, Rows: rows, Stable: stablePorts(services)}
	if proxyStatus.Running && entry.OK && (entry.Service == "" || serviceRunning(services, entry.Service)) {
		where.ProxyPort = proxyStatus.Port
	}
	chips, appWarnings, covered := entryApps(entry, inPlay, where, services)
	if kerr != nil {
		appWarnings = append(appWarnings, "knowledge cannot be loaded: "+kerr.Error())
	}
	apps := buildApps(services, info, primaryService, hasPrimary && !inPlay, covered)
	if inPlay {
		apps = append(chips, apps...)
	} else {
		if k != nil {
			for i := range apps {
				apps[i].Guessed = apps[i].Label == knowledge.ReservedLinkName
			}
		}
		apps = withLinkChips(apps, chips)
	}

	switch {
	case total == 0 || running == 0:
		return services, StateStopped, apps, appWarnings
	case running == total:
		return services, StateRunning, apps, appWarnings
	default:
		return services, StatePartial, apps, appWarnings
	}
}

// stablePorts lists each service's snapshot-published host ports, the
// fallback for a service that is not running.
func stablePorts(services []Service) []state.PortAlloc {
	var out []state.PortAlloc
	for _, s := range services {
		for _, p := range s.Ports {
			if p.Host > 0 {
				out = append(out, state.PortAlloc{Service: s.Name, Target: p.Container, Host: p.Host})
			}
		}
	}
	return out
}

func serviceRunning(services []Service, name string) bool {
	for _, s := range services {
		if s.Name == name {
			return s.State == "running"
		}
	}
	return false
}

// buildServiceEndpoints resolves one endpoint per declared port (task 3.6).
func buildServiceEndpoints(svc Service, live map[int]compose.Endpoint, hostname string, info *state.Manifest) []Endpoint {
	var out []Endpoint
	for _, p := range svc.Ports {
		if e, ok := live[p.Container]; ok {
			scheme := resolveScheme(svc.knownHTTPPorts, p.Container, e.Protocol, info.HTTPPorts, info.HTTPSPorts)
			host := e.Host
			if scheme != e.Scheme {
				// The kind-specific override (fix 3) changed the scheme
				// live docker data alone would have given; rebuild the
				// address for it (http/https resolve via hostname, not
				// the raw loopback address).
				if scheme == "http" || scheme == "https" {
					host = hostname
				} else {
					host = "127.0.0.1"
				}
			}
			ce := compose.Endpoint{Scheme: scheme, Host: host, Port: e.Port}
			out = append(out, Endpoint{Scheme: scheme, URL: ce.URL(), Container: p.Container, Known: true})
			continue
		}
		scheme := resolveScheme(svc.knownHTTPPorts, p.Container, p.Protocol, info.HTTPPorts, info.HTTPSPorts)
		if p.Host <= 0 {
			// A legacy/--random-ports service Docker hasn't assigned a
			// port to yet (it isn't running): nothing to show but which
			// container port it is.
			out = append(out, Endpoint{Scheme: scheme, Container: p.Container, Known: false})
			continue
		}
		host := "127.0.0.1"
		if scheme == "http" || scheme == "https" {
			host = hostname
		}
		ce := compose.Endpoint{Scheme: scheme, Host: host, Port: strconv.Itoa(p.Host)}
		out = append(out, Endpoint{Scheme: scheme, URL: ce.URL(), Container: p.Container, Known: true})
	}
	return out
}

// resolveScheme is compose.SchemeForTarget extended with fix 3's
// image-specific known HTTP ports: an explicit io.stackfork.http/.https
// label always wins, then a known image's own well-known UI port, then the
// generic fixed-port guess (80/443/3000/8080/5050) SchemeForTarget already
// makes. knownHTTPPorts is nil for anything but a handful of recognized
// images (service_details.go), so an unrecognized image is never guessed at.
func resolveScheme(knownHTTPPorts []int, target int, protocol string, httpPorts, httpsPorts []int) string {
	for _, p := range httpsPorts {
		if p == target {
			return "https"
		}
	}
	for _, p := range httpPorts {
		if p == target {
			return "http"
		}
	}
	for _, p := range knownHTTPPorts {
		if p == target {
			return "http"
		}
	}
	return compose.SchemeForTarget(target, protocol, nil, nil)
}

// proxyEndpoint is the extra, portless address once the shared proxy is
// actually routing an instance's primary HTTP service (design.md D7 layer
// 3/task 3.6): shown alongside the direct one, never instead of it.
func proxyEndpoint(hostname string, https bool, proxyPort, target int) Endpoint {
	scheme := "http"
	if https {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s", scheme, hostname)
	if proxyPort != 80 {
		url = fmt.Sprintf("%s:%d", url, proxyPort)
	}
	return Endpoint{Scheme: scheme, URL: url, Container: target, Known: true, Proxied: true}
}

// busyPortWarnings flags a recorded stable port (design.md D7 layer 2) that
// is bound by something other than this instance's own containers (task
// 1.3).
func busyPortWarnings(info *state.Manifest, rows []compose.StatusRow) []string {
	ownPorts := map[int]bool{}
	for _, r := range rows {
		for _, p := range r.Publishers {
			if n, ok := p["PublishedPort"].(json.Number); ok {
				if v, err := n.Int64(); err == nil {
					ownPorts[int(v)] = true
				}
			}
		}
	}
	var warnings []string
	for _, alloc := range info.Ports {
		if !ports.Bound(alloc.Host) || ownPorts[alloc.Host] {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"service %s's port %d is in use by another process; run `stackfork prepare --reassign-ports`", alloc.Service, alloc.Host))
	}
	return warnings
}

// knowledgeStatus reads <root>/.stackfork/knowledge.json, if any, and
// resolves its effective status (verified knowledge goes stale here, at
// read time, rather than being rewritten on disk).
// ReadKnowledgeStatus exposes knowledgeStatus for the CLI's `where` (task
// 13.7): the one place that resolves a project's effective knowledge
// status (verified knowledge goes stale here, at read time, never
// rewritten to disk), shared so the dashboard and the plain CLI can never
// disagree about it.
func ReadKnowledgeStatus(root string) (*KnowledgeStatus, bool) {
	return knowledgeStatus(root)
}

// ReadKnowledgeError is the error of a knowledge.json that exists but cannot
// be loaded (nil when it loads or is absent).
func ReadKnowledgeError(root string) error {
	_, err := knowledge.LoadFirst(root)
	return err
}

func knowledgeStatus(root string) (*KnowledgeStatus, bool) {
	k, err := knowledge.Load(knowledgeJSONPath(root))
	if err != nil {
		return nil, false
	}
	status := k.Status
	var changed []string
	if status == knowledge.StatusVerified {
		if changed = k.StaleInputs(root); len(changed) > 0 {
			status = knowledge.StatusStale
		}
	}
	date := ""
	if k.Verification != nil {
		date = k.Verification.Date
	}
	return &KnowledgeStatus{Status: status, Date: date, ChangedFiles: changed}, true
}

// RunningProjects reuses the same `docker ps --all` listing every other
// inventory read does, and returns the set of project names with at least
// one currently-running container - the "never touches running instances"
// guard `gc` needs (design.md D8), sharing one source of truth for
// container state rather than a second docker call with its own parsing.
func RunningProjects() (map[string]bool, error) {
	rows, err := listContainers()
	if err != nil {
		return nil, err
	}
	running := map[string]bool{}
	for _, r := range rows {
		if strings.EqualFold(r.State, "running") {
			running[r.Project] = true
		}
	}
	return running, nil
}

// --- docker ps -----------------------------------------------------------

// containerRow is one container from the single `docker ps --all` call,
// parsed just enough to build per-instance StatusRows without a
// per-instance `docker compose ps` (design.md D1: "not one call per
// instance").
type containerRow struct {
	Project, Service, State, Health string
	Publishers                      []map[string]interface{}
	ID, Name, HealthDetail          string
	OneOff                          bool // a `compose run` container, not a service replica
}

var healthPattern = regexp.MustCompile(`\((healthy|unhealthy|health: starting)\)`)

// listContainers runs one `docker ps --all` (task 1.1) and keeps only
// containers carrying either label namespace's root label
// (identity.LabelRoot or identity.LegacyLabelRoot). Docker's `--filter
// label=` ANDs distinct label keys together rather than ORing them
// (verified against this machine's own containers: filtering on both
// labels at once matched nothing, though containers exist under each
// namespace separately), so the namespace filtering happens here on the
// parsed Labels instead of at the docker ps invocation - one call either
// way.
func listContainers() ([]containerRow, error) {
	cmd := exec.Command("docker", "ps", "--all", "--format", "json")
	cmd.Env = compose.DockerEnv(nil)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("docker ps: %s", msg)
	}

	var rows []containerRow
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw struct {
			Labels string `json:"Labels"`
			State  string `json:"State"`
			Status string `json:"Status"`
			Ports  string `json:"Ports"`
			ID     string `json:"ID"`
			Names  string `json:"Names"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		labels := parseLabels(raw.Labels)
		_, hasRoot := labels[identity.LabelRoot]
		_, hasLegacyRoot := labels[identity.LegacyLabelRoot]
		if !hasRoot && !hasLegacyRoot {
			continue
		}
		row := containerRow{
			Project: labels["com.docker.compose.project"],
			Service: labels["com.docker.compose.service"],
			State:   raw.State,
			ID:      raw.ID, Name: raw.Names,
			OneOff: strings.EqualFold(labels["com.docker.compose.oneoff"], "true"),
		}
		if m := healthPattern.FindStringSubmatch(raw.Status); m != nil {
			row.Health = m[1]
		}
		row.Publishers = parsePorts(raw.Ports)
		rows = append(rows, row)
	}
	fillHealthDetails(rows)
	return rows, nil
}

var secretish = regexp.MustCompile(`(?i)(password|passwd|token|secret|api[_-]?key|authorization)(\s*[=:]\s*)\S+`)

// fillHealthDetails reads the output of the latest health check of every
// unhealthy service container with ONE `docker inspect` (Docker can be slow
// to answer, so never one call per container), keeping the last non-empty
// line, masked and shortened.
func fillHealthDetails(rows []containerRow) {
	var ids []string
	for _, r := range rows {
		if r.Health == "unhealthy" && !r.OneOff && r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	args := append([]string{"inspect", "--format", `{{.Id}} {{json .State.Health.Log}}`}, ids...)
	cmd := exec.Command("docker", args...)
	cmd.Env = compose.DockerEnv(nil)
	out, _ := cmd.Output() // best effort: a missing detail only shortens the warning
	lines := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		full, js, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok {
			continue
		}
		var log []struct{ Output string }
		if json.Unmarshal([]byte(js), &log) != nil || len(log) == 0 {
			continue
		}
		parts := strings.Split(strings.TrimSpace(log[len(log)-1].Output), "\n")
		line := secretish.ReplaceAllString(strings.TrimSpace(parts[len(parts)-1]), "$1$2***")
		if r := []rune(line); len(r) > 160 {
			line = string(r[:160]) + "..."
		}
		lines[full] = line
	}
	for i := range rows {
		for full, line := range lines {
			if rows[i].ID != "" && strings.HasPrefix(full, rows[i].ID) {
				rows[i].HealthDetail = line
			}
		}
	}
}

func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

// parsePorts parses `docker ps`'s human Ports column ("127.0.0.1:32834->5432/tcp,
// 7473/tcp") into Publishers shaped like compose ps's, so
// compose.ComputeEndpoints works unchanged on either source. Unpublished
// entries (no "->") are dropped, matching compose.PublicStatus. IPv4/IPv6
// duplicates of the same published mapping are deduplicated.
func parsePorts(s string) []map[string]interface{} {
	if s == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []map[string]interface{}
	for _, entry := range strings.Split(s, ", ") {
		entry = strings.TrimSpace(entry)
		hostPart, rest, ok := strings.Cut(entry, "->")
		if !ok {
			continue
		}
		targetProto, protocol, ok := strings.Cut(rest, "/")
		if !ok {
			protocol = "tcp"
			targetProto = rest
		}
		target, err := strconv.Atoi(targetProto)
		if err != nil {
			continue
		}
		idx := strings.LastIndex(hostPart, ":")
		if idx < 0 {
			continue
		}
		publishedPort, err := strconv.Atoi(hostPart[idx+1:])
		if err != nil {
			continue
		}
		key := fmt.Sprintf("%d/%s/%d", target, protocol, publishedPort)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, map[string]interface{}{
			"TargetPort":    json.Number(strconv.Itoa(target)),
			"PublishedPort": json.Number(strconv.Itoa(publishedPort)),
			"Protocol":      protocol,
		})
	}
	return out
}

// toStatusRows keeps only service containers: a one-off `compose run`
// container never decides a service's state or health.
func toStatusRows(rows []containerRow) []compose.StatusRow {
	out := make([]compose.StatusRow, 0, len(rows))
	for _, r := range rows {
		if r.OneOff {
			continue
		}
		out = append(out, compose.StatusRow{
			Service: r.Service, State: r.State, Health: r.Health, Publishers: r.Publishers, HealthDetail: r.HealthDetail,
		})
	}
	return out
}

// CleanupNote is what the card says about a merged or closed branch: Title is
// the callout's words, Ask names its buttons ("suggest": Clean up and Keep;
// "auto": Clean up now and Keep; "": a warning only).
type CleanupNote struct {
	Repo   string `json:"repo"` // main checkout
	Path   string `json:"path"` // the worktree
	Branch string `json:"branch"`
	Title  string `json:"title"`
	PR     int    `json:"pr,omitempty"`
	URL    string `json:"url,omitempty"`
	Ask    string `json:"ask,omitempty"`
}

// LastRun summarizes the newest run recorded for an instance. Beyond the
// fields of design.md D14 it carries what the card needs without a second
// request: the retry command, the end of the failed step's log, and the
// running step's position.
type LastRun struct {
	ID         int       `json:"id"`
	Command    string    `json:"command"`
	Trigger    string    `json:"trigger"`
	Outcome    string    `json:"outcome"`
	FailedStep string    `json:"failed_step,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitzero"`
	Retry      string    `json:"retry,omitempty"`      // failed or interrupted runs
	Tail       string    `json:"tail,omitempty"`       // last lines of the failed step's log
	Step       string    `json:"step,omitempty"`       // running run: the step running now
	StepN      int       `json:"step_n,omitempty"`     // its 1-based position
	StepTotal  int       `json:"step_total,omitempty"` // steps in the run
}

// tailLines is how many lines of a failed step's log the card shows.
const tailLines = 6

func lastRun(instanceDir string) *LastRun {
	r := runs.Latest(instanceDir)
	if r == nil {
		return nil
	}
	lr := &LastRun{ID: r.ID, Command: r.Command, Trigger: r.Trigger, Outcome: r.Outcome, FailedStep: r.FailedStep(),
		StartedAt: r.StartedAt, EndedAt: r.EndedAt}
	switch r.Outcome {
	case runs.Failed, runs.Interrupted:
		lr.Retry = r.Retry
		if lr.FailedStep != "" {
			if b, err := runs.Log(instanceDir, r.ID, lr.FailedStep); err == nil {
				lr.Tail = endOf(string(b), tailLines)
			}
		}
	case runs.Running:
		for i, s := range r.Steps {
			if s.State == runs.StepRunning {
				lr.Step, lr.StepN, lr.StepTotal = s.ID, i+1, len(r.Steps)
			}
		}
	}
	return lr
}

// endOf is the last n non-empty lines of text, trailing space trimmed.
func endOf(text string, n int) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimRight(l, " \t\r"); strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// seedRunLog is the output of the newest run that ran the seed step and the
// run and step it came from; "" when no run did (an instance seeded before
// runs existed has only seed.log).
func seedRunLog(instanceDir, root string) (text string, run int, step string) {
	main := identity.MainRepoOf(root)
	if main == "" {
		main = root
	}
	id := "seed" // the step a Start or `seed` records, as SeedEntry names it
	if st, ok := lifecycle.SeedEntry(root, main, instanceDir, lifecycle.LoadPlan(root, main), lifecycle.PlanMode(root, main)); ok {
		id = st.ID
	}
	all, _ := runs.List(instanceDir)
	for _, r := range all {
		for _, s := range r.Steps {
			if s.ID == id && s.Log != "" && (s.State == runs.Succeeded || s.State == runs.Failed) {
				if b, err := runs.Log(instanceDir, r.ID, id); err == nil {
					return string(b), r.ID, id
				}
			}
		}
	}
	return "", 0, ""
}

// InstanceRef points a repo checkout at its stackfork instance (the repo
// browser's join with the inventory).
type InstanceRef struct {
	Project string `json:"project"`
	State   string `json:"state"`
}

// fillSeed flags a failed seed: the manifest says not seeded yet, and
// there is a seed to run (recorded, or detected in the checkout). A seed
// that is only pending (the up that should run it failed) did not fail.
func fillSeed(inst *Instance, info *state.Manifest, instanceDir string) {
	if info.Seeded == nil || *info.Seeded || info.SeedPending {
		return
	}
	decided, cmd, _ := learn.SeedDecision([]string{info.Root}, info)
	if !decided && checkoutExists(info.Root) {
		_, cmd = learn.DetectSeed(info.Root)
	}
	if !learn.SeedActive(cmd) {
		return
	}
	run := "stackfork --instance " + info.Project + " seed"
	inst.SeedCommand, inst.SeedForce, inst.SeedDone = run, run+" --force", run+" --mark-done"
	if log, run, step := seedRunLog(instanceDir, info.Root); log != "" {
		inst.SeedError = lastLines(log)
		inst.SeedRun, inst.SeedStep = run, step
	} else {
		inst.SeedError = seedError(filepath.Join(instanceDir, state.SeedLog))
	}
}

// seedError is the last few lines of the failed seed's output.
func seedError(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return lastLines(string(data))
}

// lastLines is the end of a seed's output: its last 4 lines, without stack frames.
func lastLines(text string) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "at ") {
			lines = append(lines, l)
		}
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > 600 {
		out = "..." + out[len(out)-600:]
	}
	return out
}

// serviceWarnings turns the services' states into one warning per kind
// (unhealthy, exited, not started), each naming every affected service and
// the one command that recovers them.
func serviceWarnings(project string, services []Service) []string {
	type group struct {
		names  []string
		reason string
	}
	var unhealthy, exited, notStarted, waiting []group
	add := func(gs *[]group, name, reason string) {
		for i := range *gs {
			if (*gs)[i].reason == reason {
				(*gs)[i].names = append((*gs)[i].names, name)
				return
			}
		}
		*gs = append(*gs, group{names: []string{name}, reason: reason})
	}
	for _, s := range services {
		switch {
		case s.Health == "unhealthy":
			add(&unhealthy, s.Name, healthReason(s.HealthDetail))
		case s.State == "exited" || s.State == "dead" || s.State == "restarting":
			add(&exited, s.Name, s.State)
		case s.State == StateWaiting:
			add(&waiting, s.Name, "waiting for "+strings.Join(s.WaitingFor, ", ")+" to become healthy")
		case s.State == StateNotStarted:
			add(&notStarted, s.Name, "")
		}
	}
	cmd := func(verb string, names []string) string {
		return "`stackfork " + verb + strings.Join(append([]string{""}, names...), " ") + "`"
	}
	var out []string
	for _, g := range unhealthy {
		msg := strings.Join(g.names, ", ") + " unhealthy"
		if g.reason != "" {
			msg += ": " + g.reason
		}
		out = append(out, msg+" - try "+cmd("restart", g.names))
	}
	for _, g := range exited {
		out = append(out, strings.Join(g.names, ", ")+" "+g.reason+" - if that is unexpected, try "+cmd("restart", g.names))
	}
	for _, g := range waiting {
		out = append(out, strings.Join(g.names, ", ")+" "+g.reason)
	}
	for _, g := range notStarted {
		out = append(out, strings.Join(g.names, ", ")+" not started - run `stackfork up`")
	}
	return out
}

// healthReason words why a health check fails: a timeout is usually load,
// not a broken service; anything else shows the check's own last line.
func healthReason(detail string) string {
	switch {
	case strings.Contains(strings.ToLower(detail), "health check exceeded timeout"):
		return "health check timing out (Docker may be overloaded or the service is slow to answer)"
	default:
		return detail
	}
}

// fillOneOffs counts a project's `compose run` containers apart from its
// services: running ones, and leftovers (created or exited, never removed).
func fillOneOffs(inst *Instance, rows []containerRow) {
	var names []string
	for _, r := range rows {
		if !r.OneOff {
			continue
		}
		if strings.EqualFold(r.State, "running") {
			inst.OneOffRunning++
			continue
		}
		inst.OneOffLeftover++
		if r.Name != "" {
			names = append(names, r.Name)
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		inst.OneOffCleanup = "docker rm " + strings.Join(names, " ")
	}
}

// markWaiting turns a never-started service into "waiting" when something it
// depends on is still starting (health starting, or itself waiting), so a
// stack that is simply coming up in order does not look broken.
func markWaiting(services []Service) {
	byName := map[string]*Service{}
	for i := range services {
		byName[services[i].Name] = &services[i]
	}
	starting := func(s *Service) bool {
		return (s.State == "running" && s.Health == "health: starting") || s.State == StateWaiting
	}
	for changed := true; changed; {
		changed = false
		for i := range services {
			s := &services[i]
			if s.State != StateNotStarted {
				continue
			}
			var deps []string
			for _, d := range s.DependsOn {
				if dep := byName[d]; dep != nil && starting(dep) {
					deps = append(deps, d)
				}
			}
			if len(deps) > 0 {
				s.State, s.WaitingFor, changed = StateWaiting, deps, true
			}
		}
	}
}

// relRoot shortens a checkout path for display (Instance.RelRoot). A .git
// directory marks a repo's own checkout; a linked worktree has a .git file.
func relRoot(root string) string {
	if i := strings.LastIndex(root, "/.worktrees/"); i >= 0 {
		return root[i+1:]
	}
	if fi, err := os.Stat(filepath.Join(root, ".git")); err == nil && fi.IsDir() {
		return "main checkout"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		if root == home {
			return "~"
		}
		if strings.HasPrefix(root, home+"/") {
			return "~" + root[len(home):]
		}
	}
	return root
}

// appName is the folder name of the main checkout Root belongs to: the part
// before "/.worktrees/", the checkout itself when .git is a directory, or the
// main repo a linked worktree's .git file points at. "" when none applies.
func appName(root string) string {
	if i := strings.LastIndex(root, "/.worktrees/"); i > 0 {
		return filepath.Base(root[:i])
	}
	fi, err := os.Stat(filepath.Join(root, ".git"))
	if err != nil {
		return ""
	}
	if fi.IsDir() {
		return filepath.Base(root)
	}
	data, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return ""
	}
	if i := strings.Index(string(data), "/.git/worktrees/"); i > 0 {
		return filepath.Base(string(data)[:i])
	}
	return ""
}

// HostPrefix starts the service id of a host process row ("host:NAME"), so
// it can never collide with a compose service called NAME.
const HostPrefix = "host:"

// hostServices lists each recorded host process as a service row: running
// only when the identity holds and the port answers, else exited. It uses
// the manifest already loaded (no re-read per process).
func hostServices(info *state.Manifest) []Service {
	names := make([]string, 0, len(info.HostProcs))
	for n := range info.HostProcs {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Service
	for _, n := range names {
		proc := info.HostProcs[n]
		svc := Service{Name: HostPrefix + n, Label: n, Kind: "host", Image: "host", State: "exited"}
		if proc.PID != 0 && state.HostIdentity(proc) && ports.Bound(proc.Port) {
			svc.State = "running"
		}
		if proc.Port != 0 {
			svc.Endpoints = []Endpoint{{Scheme: "http", URL: fmt.Sprintf("http://127.0.0.1:%d", proc.Port), Container: proc.Port, Known: true}}
		}
		out = append(out, svc)
	}
	return out
}
