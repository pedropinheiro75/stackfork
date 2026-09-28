package compose

import (
	"bytes"
	"encoding/json"
	"strings"
)

// DecodeJSON parses JSON the way Python's json.loads does: numbers keep
// their int/float distinction (json.Number), needed to mirror Python's
// isinstance(x, int) check on port targets.
func DecodeJSON(data []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// deepCopy mirrors Python's copy.deepcopy(source) for JSON-shaped values.
// Lazy: round-trips through JSON rather than a hand-written recursive copy.
func deepCopy(v interface{}) interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // v came from DecodeJSON; must be JSON-marshalable
	}
	out, err := DecodeJSON(b)
	if err != nil {
		panic(err)
	}
	return out
}

// truthy mirrors Python truthiness for JSON-decoded values.
func truthy(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f != 0
	case string:
		return t != ""
	case []interface{}:
		return len(t) != 0
	case map[string]interface{}:
		return len(t) != 0
	default:
		return true
	}
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}

func asList(v interface{}) ([]interface{}, bool) {
	l, ok := v.([]interface{})
	return l, ok
}

func isJSONInt(v interface{}) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	_, err := n.Int64()
	return err == nil
}

// NormalizeEnvironment reads a service's raw "environment" value into a
// plain map[string]string, regardless of which shape Compose rendered it
// in. Without --no-interpolate, `docker compose config` always normalizes
// environment to a map; with it (design.md D6's security fix - required so
// an unresolved ${VAR} is never silently baked into the snapshot), a
// service whose *source* Compose file declared environment as a list
// (`- KEY=VALUE`) comes back as a list too, unnormalized - verified
// empirically, not assumed. Every reader of a service's environment must
// go through this rather than a direct map type assertion, or a
// list-declared service's env silently reads as empty. A bare "KEY" list
// entry (no "=", meaning "inherit from the shell running docker compose")
// has no resolvable value here and is skipped, same as it would be absent
// from a map-shaped environment with an unset value removed.
func NormalizeEnvironment(raw interface{}) map[string]string {
	out := map[string]string{}
	switch v := raw.(type) {
	case map[string]interface{}:
		for k, val := range v {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
	case []interface{}:
		for _, entry := range v {
			s, ok := entry.(string)
			if !ok {
				continue
			}
			k, val, hasEq := strings.Cut(s, "=")
			if !hasEq {
				continue
			}
			out[k] = val
		}
	}
	return out
}
