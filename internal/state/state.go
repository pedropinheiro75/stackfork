// Package state ports the original Python CLI's manifest read/write, private
// atomic writes (0600/0700) and the per-instance flock.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Manifest mirrors the Python `info` dict written to manifest.json.
type Manifest struct {
	Project  string `json:"project"`
	Root     string `json:"root"`
	Branch   string `json:"branch"`
	Hostname string `json:"hostname"`
	// Npmrc is read-only compatibility for a manifest written before named
	// secrets existed (task 6.2); new manifests leave it nil and store the
	// same information as Secrets["npm_token"] instead. Use ResolvedSecrets,
	// not this field directly.
	Npmrc   *string           `json:"npmrc,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty"`
	// HTTPPorts and HTTPSPorts are the target ports declared via the
	// io.stackfork.http/.https labels at prepare (task 7.2, design.md D7).
	// urls uses them ahead of its fixed-port guess.
	HTTPPorts  []int `json:"http_ports,omitempty"`
	HTTPSPorts []int `json:"https_ports,omitempty"`
	// Ports records the stable host port allocated to each published
	// (service, target port) at prepare (task 8.2, design.md D7 layer 2).
	// Empty when the instance was prepared with --random-ports, or is a
	// legacy (idv-) instance never re-prepared with --reassign-ports.
	Ports []PortAlloc `json:"ports,omitempty"`
	// Routes are host-run app registrations added via `route add <name>
	// <port>` (task 9.3, design.md D7 layer 3): name -> the port the app
	// listens on, on this instance's own hostname, routed by the shared proxy
	// to host.docker.internal:<port>. Stored per-instance since the CLI
	// already resolves one instance per checkout+branch.
	Routes map[string]int `json:"routes,omitempty"`
	// HostPorts remembers the stable port `stackfork host <name>` picked for
	// each name, so a host-run app keeps its port across runs (Routes only
	// holds it while the app runs).
	HostPorts map[string]int `json:"host_ports,omitempty"`
	// DepsHashes is, per `exec once: deps` step id, the lockfile hash of its
	// last successful run.
	DepsHashes map[string]string `json:"deps_hashes,omitempty"`
	// HostProcs are the live host processes (`host start`, or a foreground
	// `host`), keyed by name. Never trust an entry on its PID alone: see HostStatus.
	HostProcs    map[string]HostProc `json:"host_procs,omitempty"`
	Files        []string            `json:"files"`
	EnvFile      *string             `json:"env_file"`
	Inputs       map[string]string   `json:"inputs"`
	ConfigSHA256 string              `json:"config_sha256"`
	// SeedOnce is the command that seeds a fresh database (recorded from a
	// sibling worktree's manifest when no knowledge file has one), run in
	// SeedService after the first up that created the data volume. Seeded is
	// nil until that decision is made, false until the seed succeeded.
	// SeedPending means the seed is due but has not run yet (the up that
	// created the volume failed), so the next up runs it.
	SeedOnce    string `json:"seed_once,omitempty"`
	SeedService string `json:"seed_service,omitempty"`
	Seeded      *bool  `json:"seeded,omitempty"`
	SeedPending bool   `json:"seed_pending,omitempty"`
	// WantRunning is the person's last word on the instance: set by an up,
	// cleared by a down. Resume starts the ones that want to run after a
	// reboot took the containers away.
	WantRunning bool `json:"want_running,omitempty"`
}

// SetWantRunning records whether the person wants the instance running. It
// writes only a change, so the manifest's mtime (the "last used" order) moves
// only when something did. Callers hold the instance lock or write atomically
// like the seed marker does; a missing manifest is left alone.
func SetWantRunning(instanceDir string, v bool) {
	path := filepath.Join(instanceDir, "manifest.json")
	m, err := ReadManifest(path)
	if err != nil || m == nil || m.WantRunning == v {
		return
	}
	m.WantRunning = v
	_ = WritePrivate(path, m)
}

// HostProc identifies a supervised host process. PID is the supervisor's.
type HostProc struct {
	PID       int      `json:"pid"`
	StartedAt string   `json:"started_at"` // RFC3339 UTC, from ps lstart
	Cmdline   []string `json:"cmdline"`    // the supervisor's own argv
	Port      int      `json:"port"`
	Log       string   `json:"log,omitempty"`
	// Restart is the supervisor's argument list (without the executable) that
	// starts this app again detached. A foreground `host` run records it, since
	// its own Cmdline is not a supervised one; `host start` leaves it empty and
	// Cmdline[1:] serves.
	Restart []string `json:"restart,omitempty"`
}

// RestartArgs are the arguments that start the app again detached, or nil
// when it cannot be: they must read `... host --supervise NAME ... -- COMMAND...`.
// Anything else is refused, so a hand-edited manifest cannot make stackfork
// run an arbitrary subcommand.
func (p HostProc) RestartArgs() []string {
	args := p.Restart
	if len(args) == 0 && len(p.Cmdline) > 1 {
		args = p.Cmdline[1:]
	}
	for i := 1; i+1 < len(args); i++ {
		if args[i] != "--supervise" || args[i-1] != "host" || !hostNameOK(args[i+1]) {
			continue
		}
		for _, a := range args[i+2:] {
			if a == "--" {
				return args
			}
		}
	}
	return nil
}

func hostNameOK(n string) bool {
	if n == "" || !(n[0] >= 'a' && n[0] <= 'z' || n[0] >= '0' && n[0] <= '9') {
		return false
	}
	for _, c := range n {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// PortAlloc is one (service, target port) -> stable host port allocation
// made at prepare, stored so it survives down/up, container recreation and
// re-prepare (task 8.2, design.md D7 layer 2). Host is always bound to
// 127.0.0.1 (loopback only).
type PortAlloc struct {
	Service string `json:"service"`
	Target  int    `json:"target"`
	Host    int    `json:"host"`
}

// ResolvedSecrets returns the manifest's named secret references (never
// values, design.md D6), folding in the legacy Npmrc key as the npm_token
// secret (task 6.2) unless Secrets already names one explicitly.
func (m *Manifest) ResolvedSecrets() map[string]string {
	out := make(map[string]string, len(m.Secrets)+1)
	for name, ref := range m.Secrets {
		out[name] = ref
	}
	if m.Npmrc != nil {
		if _, ok := out["npm_token"]; !ok {
			out["npm_token"] = "npmrc:" + *m.Npmrc + "#npm.pkg.github.com"
		}
	}
	return out
}

// KeptSecrets is the file a removed instance leaves in its state dir: the
// secret references (never values) its manifest recorded. Removing an
// instance throws its data away, not the person's choice of where a secret
// comes from, so the next prepare of the same checkout+branch records them
// again (add-secret-vault D7: the manifest's references survive).
const KeptSecrets = "kept-secrets.json"

// RecordedSecrets are the references an instance recorded: its manifest's,
// or, when it has none, those its removal kept. Nil when neither exists.
func RecordedSecrets(instanceDir string) map[string]string {
	if m, err := ReadManifest(filepath.Join(instanceDir, "manifest.json")); err == nil && m != nil {
		return m.ResolvedSecrets()
	}
	var kept map[string]string
	if b, err := os.ReadFile(filepath.Join(instanceDir, KeptSecrets)); err == nil {
		json.Unmarshal(b, &kept) //nolint:errcheck // an unreadable file keeps nothing
	}
	return kept
}

// ForgetSecret drops the reference name from the instance's manifest and from
// its kept references, under the instance lock. It reports whether it held one.
func ForgetSecret(instanceDir, name string) (bool, error) {
	lock, err := AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return false, err
	}
	defer lock.Release()
	found := false
	path := filepath.Join(instanceDir, "manifest.json")
	if m, err := ReadManifest(path); err == nil && m != nil {
		_, found = m.Secrets[name]
		delete(m.Secrets, name)
		if name == "npm_token" && m.Npmrc != nil { // the legacy key reads as npm_token
			m.Npmrc, found = nil, true
		}
		if found {
			if err := WritePrivate(path, m); err != nil {
				return false, err
			}
		}
	}
	kp := filepath.Join(instanceDir, KeptSecrets)
	var kept map[string]string
	if b, err := os.ReadFile(kp); err == nil && json.Unmarshal(b, &kept) == nil {
		if _, ok := kept[name]; ok {
			delete(kept, name)
			if len(kept) == 0 {
				return true, os.Remove(kp)
			}
			return true, WritePrivate(kp, kept)
		}
	}
	return found, nil
}

// SeedLog is the file in an instance dir holding the last seed's output.
const SeedLog = "seed.log"

// Dir returns the per-instance state directory and ensures it exists with
// 0700 permissions, mirroring Python's `state.mkdir(parents=True,
// exist_ok=True, mode=0o700)`.
func Dir(base, project string) (string, error) {
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// WritePrivate mirrors Python's write_private(path, data): marshal with
// indent=2, write to a sibling temp file (0600, as os.CreateTemp already
// does), then atomically rename over the destination.
func WritePrivate(path string, data interface{}) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ReadManifest reads and decodes manifest.json, or returns (nil, nil) if it
// does not exist (mirroring Python's "previous = ... if manifest.is_file()
// else None").
func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ReadManifestRaw reads a JSON file into a generic map, for tests and for
// callers that need fields the Manifest struct doesn't model.
func ReadManifestRaw(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Manifests returns every manifest.json path under base whose instance
// directory starts with prefix+"-", sorted. It is the one shared
// implementation of the glob every caller that walks a whole state
// directory needs (where's prepared-instance list, the stable-port
// allocator's recorded-port scan, the proxy's route table, the inventory),
// instead of each repeating the same filepath.Glob call.
func Manifests(base, prefix string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(base, prefix+"-*", "manifest.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// SHA256File hashes a file's contents, mirroring Python's
// hashlib.sha256(path.read_bytes()).hexdigest().
func SHA256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// proxyStalePath lists host apps the proxy toggle could not restart (the
// plan is not trusted here): their env has the old URL.
func proxyStalePath(instanceDir string) string { return filepath.Join(instanceDir, "proxy-stale") }

// SetProxyStale records apps (none: clears the record).
func SetProxyStale(instanceDir string, apps []string) {
	if len(apps) == 0 {
		os.Remove(proxyStalePath(instanceDir)) //nolint:errcheck // best effort
		return
	}
	os.WriteFile(proxyStalePath(instanceDir), []byte(strings.Join(apps, "\n")+"\n"), 0o600) //nolint:errcheck // a missing warning is not worth failing for
}

// ProxyStaleApps are the recorded apps still running from before the toggle:
// started earlier than the record.
func ProxyStaleApps(instanceDir string, procs map[string]HostProc) []string {
	data, err := os.ReadFile(proxyStalePath(instanceDir))
	fi, serr := os.Stat(proxyStalePath(instanceDir))
	if err != nil || serr != nil {
		return nil
	}
	var out []string
	for _, n := range strings.Fields(string(data)) {
		p, ok := procs[n]
		if t, perr := time.Parse(time.RFC3339, p.StartedAt); ok && perr == nil && t.Before(fi.ModTime()) && HostIdentity(p) {
			out = append(out, n)
		}
	}
	return out
}
