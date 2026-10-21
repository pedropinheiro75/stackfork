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
			values[name] = v
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

// LogSecrets are the values the dashboard redacts a log with, obtained
// without a prompt: the remembered values, plus references that never
// prompt. When an op:// value is not remembered it returns ErrCheckSecrets,
// because showing the log unredacted would show the value.
func LogSecrets(instanceDir string, info *state.Manifest) ([]redact.Secret, error) {
	mapped, err := MappedSecrets(info.Root)
	if err != nil {
		return nil, err
	}
	refs, _, _ := InstanceSecrets(info, mapped)
	cached, _ := CachedSecrets(instanceDir)
	values := SecretValues{}
	need := false
	for name, ref := range refs {
		switch v, ok := cached[name]; {
		case ok:
			values[name] = v
		case strings.HasPrefix(ref, "op://"):
			need = true
		default:
			if v, _, _ := compose.CheckSecret(ref); v != "" {
				values[name] = v
			}
		}
	}
	if need {
		return nil, ErrCheckSecrets
	}
	return values.Redactions(), nil
}

// cacheKey names an instance directory however the state dir was spelled.
func cacheKey(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}
