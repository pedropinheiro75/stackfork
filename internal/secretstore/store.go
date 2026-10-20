package secretstore

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Status is where a reference stands, never a value (D4).
type Status string

const (
	Resolves    Status = "resolves"
	Missing     Status = "missing"
	Locked      Status = "locked"
	Unavailable Status = "unavailable"
	Error       Status = "error"
	// NotChecked is what Exists reports for op://, which has no read-free
	// check and would prompt.
	NotChecked Status = "not checked"
)

// MaxValueSize is the largest value Store accepts.
const MaxValueSize = 64 * 1024

// ValidateValue applies the rules of D3 to a value to store. The error
// never contains the value.
func ValidateValue(value string) error {
	switch {
	case value == "":
		return errors.New("the value is empty")
	case strings.ContainsAny(value, "\n\r\x00"):
		return errors.New("the value has a newline or NUL character")
	case len(value) > MaxValueSize:
		return fmt.Errorf("the value is larger than %d KiB", MaxValueSize/1024)
	}
	return nil
}

// Backend resolves keychain: and op:// references. The zero value uses the
// fixed tool paths of the running OS.
type Backend struct {
	// GOOS overrides runtime.GOOS (tests).
	GOOS string
	// Security, SecretTool and OpPaths replace the fixed tool locations
	// (tests point them at a recording helper; nothing else sets them).
	Security, SecretTool string
	OpPaths              []string
	// ScannedRoots lists the repository roots a store tool must not live
	// in. The CLI sets it from the config; nil means none.
	ScannedRoots func() []string
	// Timeout overrides the per-call timeout (tests).
	Timeout time.Duration
}

// Default is the backend the package-level functions use.
var Default = &Backend{}

// Resolve reads the value of a keychain: or op:// reference. When the
// status is not Resolves, the value is empty and err says why, without the
// value or the unparsed reference.
func Resolve(ref string) (string, Status, error) { return Default.Resolve(ref) }

// Exists reports a status without reading a value that needs a prompt.
func Exists(ref string) Status { return Default.Exists(ref) }

// Store writes value under the keychain item (create or update).
func Store(item, value string) error { return Default.Store(item, value) }

func (b *Backend) goos() string {
	if b.GOOS != "" {
		return b.GOOS
	}
	return runtime.GOOS
}

var errWindows = errors.New("not supported on Windows yet")

func (b *Backend) Resolve(ref string) (string, Status, error) {
	kind, item, err := ParseRef(ref)
	if err != nil {
		return "", Error, err
	}
	if b.goos() == "windows" {
		return "", Unavailable, errWindows
	}
	if kind == KindKeychain {
		return b.readKeychain(item, true)
	}
	return b.readOP(item)
}

func (b *Backend) Exists(ref string) Status {
	kind, item, err := ParseRef(ref)
	switch {
	case err != nil:
		return Error
	case b.goos() == "windows":
		return Unavailable
	case kind == KindOP:
		return NotChecked
	}
	_, st, _ := b.readKeychain(item, false)
	return st
}

func (b *Backend) Store(item, value string) error {
	if !ValidItem(item) {
		return errors.New("keychain item name is not valid")
	}
	if err := ValidateValue(value); err != nil {
		return err
	}
	if b.goos() == "windows" {
		return errWindows
	}
	return b.writeKeychain(item, value)
}

var errNoBackend = errors.New("store backend not available")

func (b *Backend) roots() []string {
	if b.ScannedRoots == nil {
		return nil
	}
	return b.ScannedRoots()
}

func (b *Backend) timeout(d time.Duration) time.Duration {
	if b.Timeout > 0 {
		return b.Timeout
	}
	return d
}

// keychainTool resolves the credential-store tool of this OS (D2).
func (b *Backend) keychainTool() (string, error) {
	switch b.goos() {
	case "darwin":
		return resolveTool(orPaths(b.Security, securityPaths), b.roots())
	case "linux":
		return resolveTool(orPaths(b.SecretTool, secretToolPaths), b.roots())
	}
	return "", fmt.Errorf("no credential store backend for %s", b.goos())
}

func (b *Backend) opTool() (string, error) {
	if b.OpPaths != nil {
		return resolveTool(b.OpPaths, b.roots())
	}
	return resolveTool(opPaths, b.roots())
}

func orPaths(override string, fixed []string) []string {
	if override != "" {
		return []string{override}
	}
	return fixed
}
