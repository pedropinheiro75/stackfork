// Package state ports the original Python CLI's manifest read/write, private
// atomic writes (0600/0700) and the per-instance flock.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
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
	Ports        []PortAlloc       `json:"ports,omitempty"`
	Files        []string          `json:"files"`
	EnvFile      *string           `json:"env_file"`
	Inputs       map[string]string `json:"inputs"`
	ConfigSHA256 string            `json:"config_sha256"`
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

// Lock is a held exclusive flock on a state directory's .lock file.
type Lock struct {
	file *os.File
}

// AcquireLock mirrors Python's `with (state / '.lock').open('a') as lock:
// fcntl.flock(lock, fcntl.LOCK_EX)`. Blocks until the lock is available.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Release unlocks and closes the lock file.
func (l *Lock) Release() error {
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
