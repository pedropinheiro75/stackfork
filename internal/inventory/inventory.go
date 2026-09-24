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

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
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
}

// Endpoint is one published port, ready to render: a link for http/https, a
// copyable address otherwise.
type Endpoint struct {
	Scheme string `json:"scheme"`
	URL    string `json:"url"`
}

// Service is one Compose service's current row within an instance.
type Service struct {
	Name      string     `json:"name"`
	State     string     `json:"state"`
	Health    string     `json:"health,omitempty"`
	Endpoints []Endpoint `json:"endpoints,omitempty"`
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
	Legacy   bool   `json:"legacy"`
	State    string `json:"state"`

	Services  []Service        `json:"services,omitempty"`
	Knowledge *KnowledgeStatus `json:"knowledge,omitempty"`
	Warnings  []string         `json:"warnings,omitempty"`
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
		inst := buildInstance(e.path, e.legacy, byProject[filepath.Base(filepath.Dir(e.path))])
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
func buildInstance(manifestPath string, legacy bool, rows []containerRow) Instance {
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
		Project: info.Project, Root: info.Root, Branch: info.Branch,
		Hostname: hostname, Legacy: legacy,
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
		services, serviceState := servicesForInstance(manifestPath, statusRows, hostname, info)
		inst.Services = services
		inst.State = serviceState
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

// servicesForInstance reads the instance's snapshot for its declared
// service list (so a never-started service still gets a row) and matches
// each one against its container, if any.
func servicesForInstance(manifestPath string, rows []compose.StatusRow, hostname string, info *state.Manifest) ([]Service, string) {
	instanceDir := filepath.Dir(manifestPath)
	configData, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil, StateStopped
	}
	model, err := compose.DecodeJSON(configData)
	if err != nil {
		return nil, StateStopped
	}
	m, _ := model.(map[string]interface{})
	svcMap, _ := m["services"].(map[string]interface{})
	names := make([]string, 0, len(svcMap))
	for name := range svcMap {
		names = append(names, name)
	}
	sort.Strings(names)

	endpointsByService := map[string][]compose.Endpoint{}
	for _, e := range compose.ComputeEndpoints(rows, hostname, info.HTTPPorts, info.HTTPSPorts) {
		endpointsByService[e.Service] = append(endpointsByService[e.Service], e)
	}
	rowByService := map[string]compose.StatusRow{}
	for _, r := range rows {
		rowByService[r.Service] = r
	}

	var services []Service
	running, total := 0, len(names)
	for _, name := range names {
		svc := Service{Name: name, State: StateStopped}
		if row, ok := rowByService[name]; ok {
			svc.State = strings.ToLower(row.State)
			svc.Health = row.Health
			if svc.State == "running" {
				running++
			}
		}
		for _, e := range endpointsByService[name] {
			svc.Endpoints = append(svc.Endpoints, Endpoint{Scheme: e.Scheme, URL: e.URL()})
		}
		services = append(services, svc)
	}

	switch {
	case total == 0 || running == 0:
		return services, StateStopped
	case running == total:
		return services, StateRunning
	default:
		return services, StatePartial
	}
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
func knowledgeStatus(root string) (*KnowledgeStatus, bool) {
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	k, err := knowledge.Load(path)
	if err != nil {
		return nil, false
	}
	status := k.Status
	var changed []string
	if status == knowledge.StatusVerified {
		if changed = k.StaleInputs(); len(changed) > 0 {
			status = knowledge.StatusStale
		}
	}
	date := ""
	if k.Verification != nil {
		date = k.Verification.Date
	}
	return &KnowledgeStatus{Status: status, Date: date, ChangedFiles: changed}, true
}

// --- docker ps -----------------------------------------------------------

// containerRow is one container from the single `docker ps --all` call,
// parsed just enough to build per-instance StatusRows without a
// per-instance `docker compose ps` (design.md D1: "not one call per
// instance").
type containerRow struct {
	Project, Service, State, Health string
	Publishers                      []map[string]interface{}
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
	cmd.Env = compose.DockerEnv()
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
		}
		if m := healthPattern.FindStringSubmatch(raw.Status); m != nil {
			row.Health = m[1]
		}
		row.Publishers = parsePorts(raw.Ports)
		rows = append(rows, row)
	}
	return rows, nil
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

func toStatusRows(rows []containerRow) []compose.StatusRow {
	out := make([]compose.StatusRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, compose.StatusRow{
			Service: r.Service, State: r.State, Health: r.Health, Publishers: r.Publishers,
		})
	}
	return out
}
