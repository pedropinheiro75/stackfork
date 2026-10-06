// Package prune plans and executes removal of whole stackfork instances
// (containers, networks, built images, volumes, state, proxy routes). The
// planner is pure over parsed Docker listings; the executor only ever
// touches objects that match a known instance exactly (see ownership
// helpers), never by name prefix and never with `docker system prune`.
package prune

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
)

const composeProjectLabel = "com.docker.compose.project"

// Scopes selects which instances a prune targets; they combine as a union.
type Scopes struct{ Orphaned, Stopped, All bool }

// Any reports whether at least one scope was chosen.
func (s Scopes) Any() bool { return s.Orphaned || s.Stopped || s.All }

// Candidate is one instance and everything that would be removed with it.
type Candidate struct {
	Project  string `json:"project"`
	State    string `json:"state"`
	Root     string `json:"root"`
	Branch   string `json:"branch"`
	Legacy   bool   `json:"legacy"`
	Running  bool   `json:"running"`
	Orphaned bool   `json:"orphaned"`

	StateDir   string   `json:"state_dir"`
	Containers []string `json:"containers,omitempty"` // ids
	Networks   []string `json:"networks,omitempty"`
	Volumes    []string `json:"volumes,omitempty"`
	Images     []string `json:"images,omitempty"` // repository:tag

	// LastActivity is the zero time when unknown.
	LastActivity time.Time `json:"last_activity"`
	// Best-effort sizes; SizePartial is set when some size was unknown.
	VolumeBytes int64 `json:"volume_bytes"`
	ImageBytes  int64 `json:"image_bytes"`
	SizePartial bool  `json:"size_partial,omitempty"`
}

// Plan is every prunable instance on the machine plus the shared proxy
// container (by id, empty when absent).
type Plan struct {
	Candidates []Candidate `json:"candidates"`
	ProxyID    string      `json:"-"`
	ProxyName  string      `json:"proxy_name,omitempty"`
}

// Groups counts how many candidates each scope would select.
func (p Plan) Groups(now time.Time) (orphaned, stopped, all int) {
	return len(Select(p.Candidates, Scopes{Orphaned: true}, 0, now)),
		len(Select(p.Candidates, Scopes{Stopped: true}, 0, now)),
		len(p.Candidates)
}

// Select applies the scopes (union) and the older-than filter. An instance
// with unknown activity is never selected by olderThan.
func Select(all []Candidate, s Scopes, olderThan time.Duration, now time.Time) []Candidate {
	var out []Candidate
	for _, c := range all {
		ok := s.All || (s.Stopped && !c.Running) || (s.Orphaned && c.Orphaned && !c.Running)
		if !ok {
			continue
		}
		if olderThan > 0 {
			if c.Running || c.LastActivity.IsZero() || now.Sub(c.LastActivity) <= olderThan {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// ParseDuration accepts Go durations plus `d` (days) and `w` (weeks).
func ParseDuration(s string) (time.Duration, error) {
	if m := regexp.MustCompile(`^(\d+)([dw])$`).FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := 24 * time.Hour
		if m[2] == "w" {
			unit = 7 * 24 * time.Hour
		}
		if n == 0 {
			return 0, fmt.Errorf("invalid duration %q: must be positive", s)
		}
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 48h, 7d, 2w)", s)
	}
	return d, nil
}

// Reclaim is the estimated disk freed by removing c.
func (c Candidate) Reclaim(keepVolumes bool) int64 {
	if keepVolumes {
		return c.ImageBytes
	}
	return c.VolumeBytes + c.ImageBytes
}

// Totals sums the reclaim estimate over selected candidates.
func Totals(sel []Candidate, keepVolumes bool) (bytes int64, partial bool) {
	for _, c := range sel {
		bytes += c.Reclaim(keepVolumes)
		partial = partial || c.SizePartial
	}
	return
}

// --- docker listings ------------------------------------------------------

// Container, Network, Volume and Image are the parsed `docker ... --format json` rows.
type Container struct {
	ID, Name, State string
	Labels          map[string]string
}
type Network struct {
	ID, Name string
	Labels   map[string]string
}
type Volume struct {
	Name   string
	Labels map[string]string
}
type Image struct{ Ref string }

// Listings is everything the planner needs from Docker.
type Listings struct {
	Containers []Container
	Networks   []Network
	Volumes    []Volume
	Images     []Image
	// Sizes: volume name and image ref -> bytes, from `docker system df -v`
	// (missing entries are unknown). Activity: container id -> last
	// start/finish; Running ids count as now.
	VolumeSize map[string]int64
	ImageSize  map[string]int64
	Activity   map[string]time.Time
}

func hasStackforkRoot(l map[string]string) bool {
	_, a := l[identity.LabelRoot]
	_, b := l[identity.LegacyLabelRoot]
	return a || b
}

// ownedContainers: stackfork-labelled and in exactly this compose project.
func ownedContainers(cs []Container, project string) []Container {
	var out []Container
	for _, c := range cs {
		if hasStackforkRoot(c.Labels) && c.Labels[composeProjectLabel] == project {
			out = append(out, c)
		}
	}
	return out
}

// snapshotImages returns the exact image refs stackfork built for this
// instance: services in its snapshot whose image is `<project>-<service>:dev`.
func snapshotImages(instanceDir, project string) []string {
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil
	}
	var snap struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if json.Unmarshal(data, &snap) != nil {
		return nil
	}
	var out []string
	for name, svc := range snap.Services {
		want := fmt.Sprintf("%s-%s:dev", project, name)
		if svc.Image == want {
			out = append(out, want)
		}
	}
	sort.Strings(out)
	return out
}

// BuildPlan turns the inventory and Docker listings into candidates.
func BuildPlan(inv inventory.Inventory, newBase, legacyBase string, l Listings, now time.Time) Plan {
	var plan Plan
	for _, c := range l.Containers {
		if c.Labels[identity.LabelProxy] != "" && c.Name == proxy.ContainerName() {
			plan.ProxyID, plan.ProxyName = c.ID, c.Name
		}
	}
	imageListed := map[string]bool{}
	for _, im := range l.Images {
		imageListed[im.Ref] = true
	}
	for _, inst := range inv.Instances {
		if inst.State == inventory.StateUnknown {
			continue // manifest unreadable: never guess
		}
		base := newBase
		if identity.IsLegacyInstance(inst.Project) {
			base = legacyBase
		}
		dir := filepath.Join(base, inst.Project)
		c := Candidate{
			Project: inst.Project, State: inst.State, Root: inst.Root, Branch: inst.Branch,
			Legacy: inst.Legacy, StateDir: dir,
			Orphaned: inst.State == inventory.StateMissingCheckout || inst.State == inventory.StateMissingBranch,
		}
		last := newest(mtime(filepath.Join(dir, "manifest.json")), mtime(dir))
		for _, ct := range ownedContainers(l.Containers, inst.Project) {
			if ct.ID != "" {
				c.Containers = append(c.Containers, ct.ID)
			}
			if strings.EqualFold(ct.State, "running") {
				c.Running = true
			}
			last = newest(last, l.Activity[ct.ID])
		}
		if c.Running {
			last = now
		}
		c.LastActivity = last
		for _, n := range l.Networks {
			if n.Name != "" && n.Labels[composeProjectLabel] == inst.Project {
				c.Networks = append(c.Networks, n.Name)
			}
		}
		for _, v := range l.Volumes {
			if v.Name != "" && v.Labels[composeProjectLabel] == inst.Project {
				c.Volumes = append(c.Volumes, v.Name)
			}
		}
		for _, ref := range snapshotImages(dir, inst.Project) {
			if imageListed[ref] {
				c.Images = append(c.Images, ref)
			}
		}
		for _, v := range c.Volumes {
			if n, ok := l.VolumeSize[v]; ok {
				c.VolumeBytes += n
			} else {
				c.SizePartial = true
			}
		}
		for _, im := range c.Images {
			if n, ok := l.ImageSize[im]; ok {
				c.ImageBytes += n
			} else {
				c.SizePartial = true
			}
		}
		plan.Candidates = append(plan.Candidates, c)
	}
	return plan
}

func mtime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func newest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}

// ParseSize reads Docker's human sizes ("190MB", "11.16kB", "1.2GiB").
func ParseSize(s string) (int64, bool) {
	m := regexp.MustCompile(`^([0-9.]+)\s*([A-Za-z]*)$`).FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	mult := map[string]float64{"": 1, "b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40}
	u, ok := mult[strings.ToLower(m[2])]
	if !ok {
		return 0, false
	}
	return int64(f * u), true
}

// FormatSize renders bytes for the table.
func FormatSize(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1f kB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}

// --- docker -----------------------------------------------------------------

// Run is the one place prune calls Docker.
func Run(args ...string) ([]byte, error) { return run(0, args...) }

// run is Run with an optional timeout (0 = none); `docker system df -v`
// can take tens of seconds on a machine with many volumes.
func run(timeout time.Duration, args ...string) ([]byte, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = compose.DockerEnv()
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("docker %s: %s", args[0], msg)
	}
	return out.Bytes(), nil
}

func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out
}

func jsonLines(data []byte, fn func(m map[string]interface{})) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal([]byte(line), &m) == nil {
			fn(m)
		}
	}
}

func str(m map[string]interface{}, k string) string { s, _ := m[k].(string); return s }

// List reads every listing the planner needs. Containers are required;
// the rest (and sizes/activity) degrade to empty on failure. Image sizes come
// from `image ls`; volume sizes need the slow `docker system df -v`, so they
// are read only when withVolumeSizes is set (a dry run), with a timeout.
func List(withVolumeSizes bool) (Listings, error) {
	l := Listings{VolumeSize: map[string]int64{}, ImageSize: map[string]int64{}}
	out, err := Run("ps", "--all", "--no-trunc", "--format", "json")
	if err != nil {
		return l, err
	}
	jsonLines(out, func(m map[string]interface{}) {
		l.Containers = append(l.Containers, Container{
			ID: str(m, "ID"), Name: str(m, "Names"), State: str(m, "State"), Labels: parseLabels(str(m, "Labels")),
		})
	})
	if out, err := Run("network", "ls", "--no-trunc", "--format", "json"); err == nil {
		jsonLines(out, func(m map[string]interface{}) {
			l.Networks = append(l.Networks, Network{ID: str(m, "ID"), Name: str(m, "Name"), Labels: parseLabels(str(m, "Labels"))})
		})
	}
	if out, err := Run("volume", "ls", "--format", "json"); err == nil {
		jsonLines(out, func(m map[string]interface{}) {
			l.Volumes = append(l.Volumes, Volume{Name: str(m, "Name"), Labels: parseLabels(str(m, "Labels"))})
		})
	}
	if out, err := Run("image", "ls", "--format", "json"); err == nil {
		jsonLines(out, func(m map[string]interface{}) {
			if repo, tag := str(m, "Repository"), str(m, "Tag"); repo != "" && tag != "" {
				l.Images = append(l.Images, Image{Ref: repo + ":" + tag})
				if n, ok := ParseSize(str(m, "Size")); ok {
					l.ImageSize[repo+":"+tag] = n
				}
			}
		})
	}
	if withVolumeSizes {
		if out, err := run(45*time.Second, "system", "df", "-v", "--format", "json"); err == nil {
			var df struct {
				Volumes []map[string]interface{} `json:"Volumes"`
			}
			if json.Unmarshal(out, &df) == nil {
				for _, m := range df.Volumes {
					if n, ok := ParseSize(str(m, "Size")); ok {
						l.VolumeSize[str(m, "Name")] = n
					}
				}
			}
		}
	}
	l.Activity = activity(l.Containers)
	return l, nil
}

// activity asks Docker once for the finish (or start) time of every
// stackfork-labelled container. Best effort.
func activity(cs []Container) map[string]time.Time {
	out := map[string]time.Time{}
	var ids []string
	for _, c := range cs {
		if hasStackforkRoot(c.Labels) {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return out
	}
	args := append([]string{"inspect", "-f", "{{.Id}} {{.State.StartedAt}} {{.State.FinishedAt}}"}, ids...)
	data, _ := Run(args...)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		var t time.Time
		for _, s := range f[1:] {
			if p, err := time.Parse(time.RFC3339Nano, s); err == nil && p.Year() > 1 {
				t = newest(t, p)
			}
		}
		out[f[0]] = t
	}
	return out
}

// Result is one instance's outcome.
type Result struct {
	Project string   `json:"project"`
	Removed bool     `json:"removed"`
	Errors  []string `json:"errors,omitempty"`
}

// Options for Execute.
type Options struct {
	KeepVolumes bool
	All         bool // also remove the proxy container
	NewBase     string
	LegacyBase  string
}

// Execute removes each candidate. Only ids/names collected by BuildPlan
// (exact ownership) are passed to Docker. A failure leaves that instance's
// state directory in place so the run can be repeated.
func Execute(sel []Candidate, plan Plan, opts Options, stderr io.Writer) []Result {
	var results []Result
	for _, c := range sel {
		r := Result{Project: c.Project}
		fail := func(err error) {
			if err != nil {
				r.Errors = append(r.Errors, err.Error())
			}
		}
		if c.Running {
			_, err := Run(append([]string{"stop"}, c.Containers...)...)
			fail(err)
		}
		if len(c.Containers) > 0 {
			_, err := Run(append([]string{"rm", "-f"}, c.Containers...)...)
			fail(err)
		}
		for _, n := range c.Networks {
			_, err := Run("network", "rm", n)
			fail(err)
		}
		if !opts.KeepVolumes {
			for _, v := range c.Volumes {
				_, err := Run("volume", "rm", v)
				fail(err)
			}
		}
		for _, im := range c.Images {
			_, err := Run("image", "rm", im) // no -f: an image still in use stays
			fail(err)
		}
		if len(r.Errors) == 0 {
			if err := os.RemoveAll(c.StateDir); err != nil {
				fail(fmt.Errorf("state removal failed: %w", err))
			}
		}
		r.Removed = len(r.Errors) == 0
		results = append(results, r)
	}
	if opts.All && plan.ProxyID != "" {
		if _, err := Run("rm", "-f", plan.ProxyID); err != nil {
			results = append(results, Result{Project: plan.ProxyName, Errors: []string{err.Error()}})
		} else {
			results = append(results, Result{Project: plan.ProxyName, Removed: true})
		}
	} else if len(sel) > 0 {
		lifecycle.ReloadProxyIfRunning(stderr, opts.NewBase, opts.LegacyBase)
	}
	return results
}
