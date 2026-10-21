package lifecycle

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SecretCacheTTL is how long the dashboard keeps the values of its last run
// or check, for redaction only (design D11).
const SecretCacheTTL = 8 * time.Hour

// ErrCheckSecrets is what the log view says when it cannot redact without a
// prompt: the person starts a check and the prompt comes on their click.
var ErrCheckSecrets = errors.New("Check secrets to view logs")

var secretCache = struct {
	sync.Mutex
	m   map[string]cachedValues
	now func() time.Time
}{m: map[string]cachedValues{}, now: time.Now}

type cachedValues struct {
	values SecretValues
	at     time.Time
}

// CacheSecrets remembers the values of an instance, in memory only.
func CacheSecrets(instanceDir string, v SecretValues) {
	secretCache.Lock()
	defer secretCache.Unlock()
	secretCache.m[cacheKey(instanceDir)] = cachedValues{v, secretCache.now()}
}

// CachedSecrets returns the remembered values of an instance, if they are
// younger than SecretCacheTTL.
func CachedSecrets(instanceDir string) (SecretValues, bool) {
	secretCache.Lock()
	defer secretCache.Unlock()
	instanceDir = cacheKey(instanceDir)
	c, ok := secretCache.m[instanceDir]
	if ok && secretCache.now().Sub(c.at) > SecretCacheTTL {
		delete(secretCache.m, instanceDir)
		return nil, false
	}
	return c.values, ok
}

// CheckSecrets resolves every effective reference of an instance (this may
// prompt, e.g. 1Password) and remembers the values for redaction. The values
// are returned only so a caller can redact with them.
func CheckSecrets(instanceDir string, info *state.Manifest) (SecretValues, error) {
	values, _, err := CheckSecretsStatus(instanceDir, info)
	return values, err
}

// CheckSecretsStatus is CheckSecrets that also returns the status of each
// reference it read, keyed by reference, so a page can show the outcome
// without resolving anything twice.
func CheckSecretsStatus(instanceDir string, info *state.Manifest) (SecretValues, map[string]RefStatus, error) {
	mapped, err := MappedSecrets(info.Root)
	if err != nil {
		return nil, nil, err
	}
	refs, _, _ := InstanceSecrets(info, mapped)
	values := SecretValues{}
	status := map[string]RefStatus{}
	for name, ref := range refs {
		v, st, serr := compose.CheckSecret(ref)
		if v != "" {
			values[name] = SecretValue{Ref: ref, Value: v}
		}
		rs := RefStatus{Status: st}
		if serr != nil && st != secretstore.Resolves {
			rs.Detail = firstLineOf(serr.Error())
		}
		status[ref] = rs
	}
	CacheSecrets(instanceDir, values)
	return values, status, nil
}

// gateError is ErrCheckSecrets with a sentence per reason, for the page.
type gateError struct{ detail []string }

func (e gateError) Error() string {
	return strings.Join(append([]string{ErrCheckSecrets.Error()}, e.detail...), "\n")
}

func (e gateError) Is(target error) bool { return target == ErrCheckSecrets }

// LogSecrets are the values the dashboard redacts a log with, obtained
// without a prompt: the remembered values of the same reference, plus
// references that resolve without one. Any reference that yields no value
// and has none remembered (an op:// that needs a prompt, an env: or npmrc:
// ${VAR} the dashboard cannot see, a locked or missing keychain item)
// returns ErrCheckSecrets: showing the log unredacted could show the value
// (design D11, review I4).
func LogSecrets(instanceDir string, info *state.Manifest) ([]redact.Secret, error) {
	mapped, err := MappedSecrets(info.Root)
	if err != nil {
		return nil, err
	}
	refs, _, _ := InstanceSecrets(info, mapped)
	cached, _ := CachedSecrets(instanceDir)
	values := SecretValues{}
	var env, keychain bool
	gated := false
	for name, ref := range refs {
		if c, ok := cached[name]; ok && c.Ref == ref { // a value of another reference is not this one's
			values[name] = c
			continue
		}
		if !strings.HasPrefix(ref, "op://") {
			if v, _, _ := compose.CheckSecret(ref); v != "" {
				values[name] = SecretValue{Ref: ref, Value: v}
				continue
			}
		}
		gated = true
		switch {
		case strings.HasPrefix(ref, "env:"), strings.HasPrefix(ref, "npmrc:"):
			env = true
		case strings.HasPrefix(ref, "keychain:"):
			keychain = true
		}
	}
	if gated {
		var e gateError
		if env {
			e.detail = append(e.detail, "Some secrets come from your shell environment, which the dashboard does not see. Start the environment from this page, or move each value to the keychain (stackfork secret set ITEM, then stackfork secret map REPO NAME=keychain:ITEM).")
		}
		if keychain {
			e.detail = append(e.detail, "Unlock the keychain, or store the missing item, then check again.")
		}
		return nil, e
	}
	return values.Redactions(), nil
}

// OutputSecrets are the values `run` and `exec` redact their output with,
// best effort (review I2): what the caller resolved, else what the dashboard
// remembered for the same reference, else what resolves without a prompt
// (env:, npmrc:, keychain:). An op:// value nobody resolved is not known and
// not redacted: SECURITY.md states it.
func OutputSecrets(instanceDir string, info *state.Manifest, resolved SecretValues) []redact.Secret {
	mapped, _ := MappedSecrets(info.Root)
	refs, _, _ := InstanceSecrets(info, mapped)
	cached, _ := CachedSecrets(instanceDir)
	values := SecretValues{}
	for name, ref := range refs {
		switch {
		case resolved[name].Ref == ref && resolved[name].Value != "":
			values[name] = resolved[name]
		case cached[name].Ref == ref && cached[name].Value != "":
			values[name] = cached[name]
		case !strings.HasPrefix(ref, "op://"):
			if v, _, _ := compose.CheckSecret(ref); v != "" {
				values[name] = SecretValue{Ref: ref, Value: v}
			}
		}
	}
	return values.Redactions()
}

// cacheKey names an instance directory however the state dir was spelled.
func cacheKey(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}
