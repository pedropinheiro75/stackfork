package compose

import (
	"bytes"
	"encoding/json"
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
