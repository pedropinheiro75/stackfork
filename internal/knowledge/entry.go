package knowledge

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Entry is the optional `entry` object of knowledge.json: where the app is
// (entry-links D1). Port is the container (target) port, never a published
// host port. None records "this project has no web entry"; Proposed marks
// the scan's guess, which only `learn record entry` clears.
type Entry struct {
	Service  string `json:"service,omitempty"`
	Port     int    `json:"port,omitempty"`
	Path     string `json:"path,omitempty"`
	None     bool   `json:"none,omitempty"`
	Proposed bool   `json:"proposed,omitempty"`
}

// Link is one named quick link: a path on the entry (Path alone, or Port
// with no Service for another port of the entry's service), or a service of
// the stack with its container port and an optional path.
type Link struct {
	Name    string `json:"name"`
	Service string `json:"service,omitempty"`
	Port    int    `json:"port,omitempty"`
	Path    string `json:"path,omitempty"`
}

const (
	maxLinks     = 16
	maxLinkName  = 32
	maxEntryPath = 256
	// ReservedLinkName is the entry's own chip label.
	ReservedLinkName = "App"
)

var serviceNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// ValidEntryService reports whether s is a Compose service name.
func ValidEntryService(s string) bool { return serviceNameRE.MatchString(s) }

// validEntryPath keeps every URL on the instance's host: one leading "/",
// printable ASCII, no backslash, no dot segment. The error never echoes p.
func validEntryPath(p string) error {
	switch {
	case p == "" || p[0] != '/':
		return fmt.Errorf("path must start with \"/\"")
	case strings.HasPrefix(p, "//"):
		return fmt.Errorf("path must not start with \"//\"")
	case len(p) > maxEntryPath:
		return fmt.Errorf("path is longer than %d bytes", maxEntryPath)
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x21 || c > 0x7e || c == '\\' {
			return fmt.Errorf("path has a space, control, backslash or non-ASCII character")
		}
	}
	before, _, _ := strings.Cut(strings.SplitN(p, "#", 2)[0], "?")
	for _, seg := range strings.Split(before, "/") {
		switch strings.ToLower(seg) {
		case "..", ".%2e", "%2e.", "%2e%2e":
			return fmt.Errorf("path has a \"..\" segment")
		}
	}
	return nil
}

func validLinkName(n string) error {
	switch {
	case n == "":
		return fmt.Errorf("name is required")
	case len([]rune(n)) > maxLinkName:
		return fmt.Errorf("name is longer than %d characters", maxLinkName)
	case n != strings.TrimSpace(n):
		return fmt.Errorf("name has a leading or trailing space")
	case strings.EqualFold(n, ReservedLinkName):
		return fmt.Errorf("name %q is reserved for the entry", ReservedLinkName)
	}
	for _, r := range n {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" -_.()/", r) {
			return fmt.Errorf("name may hold letters, digits, space and - _ . ( ) / only")
		}
	}
	return nil
}

func validTarget(label, service string, port int) error {
	if service != "" && !ValidEntryService(service) {
		return fmt.Errorf("%s: service is not a Compose service name", label)
	}
	if port != 0 && (port < 1 || port > 65535) {
		return fmt.Errorf("%s: port must be 1 to 65535", label)
	}
	return nil
}

// ValidateEntryLinks checks e and links against the rules of entry-links D2.
// Errors name the field and the index, never the value.
func ValidateEntryLinks(e *Entry, links []Link) error {
	if e != nil {
		switch {
		case e.None:
			if e.Service != "" || e.Port != 0 || e.Path != "" || e.Proposed {
				return fmt.Errorf("entry: none cannot be combined with other keys")
			}
		default:
			if e.Service == "" || e.Port == 0 {
				return fmt.Errorf("entry: service and port are required (or none)")
			}
			if err := validTarget("entry", e.Service, e.Port); err != nil {
				return err
			}
			if e.Path != "" {
				if err := validEntryPath(e.Path); err != nil {
					return fmt.Errorf("entry: %w", err)
				}
			}
		}
	}
	if len(links) > maxLinks {
		return fmt.Errorf("links: at most %d links", maxLinks)
	}
	seen := map[string]bool{}
	for i, l := range links {
		label := fmt.Sprintf("links[%d]", i)
		if err := validLinkName(l.Name); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if lower := strings.ToLower(l.Name); seen[lower] {
			return fmt.Errorf("%s: name is used twice", label)
		} else {
			seen[lower] = true
		}
		if err := validTarget(label, l.Service, l.Port); err != nil {
			return err
		}
		if l.Service != "" && l.Port == 0 {
			return fmt.Errorf("%s: a service link needs a port", label)
		}
		if l.Service == "" && l.Port == 0 && l.Path == "" {
			return fmt.Errorf("%s: needs a path, or a service and port", label)
		}
		if l.Path != "" {
			if err := validEntryPath(l.Path); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
		}
	}
	return nil
}

// objectKeys decodes raw as a JSON object with only the allowed keys.
func objectKeys(raw json.RawMessage, label string, allowed ...string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ok := false
		for _, a := range allowed {
			ok = ok || k == a
		}
		if !ok {
			return nil, fmt.Errorf("%s: key %q is not allowed (allowed: %s)", label, k, strings.Join(allowed, ", "))
		}
	}
	return obj, nil
}

// field decodes obj[key] into dst; a wrong type is an error that does not
// echo the value.
func field(obj map[string]json.RawMessage, label, key string, dst interface{}) error {
	v, ok := obj[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(v, dst); err != nil {
		return fmt.Errorf("%s: %s has the wrong type", label, key)
	}
	if p, isInt := dst.(*int); isInt && (*p < 1 || *p > 65535) {
		return fmt.Errorf("%s: port must be 1 to 65535", label)
	}
	return nil
}

// parseEntry decodes the "entry" key strictly.
func parseEntry(raw json.RawMessage) (*Entry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	obj, err := objectKeys(raw, "entry", "service", "port", "path", "none", "proposed")
	if err != nil {
		return nil, err
	}
	var e Entry
	for _, f := range []struct {
		key string
		dst interface{}
	}{{"service", &e.Service}, {"port", &e.Port}, {"path", &e.Path}, {"none", &e.None}, {"proposed", &e.Proposed}} {
		if err := field(obj, "entry", f.key, f.dst); err != nil {
			return nil, err
		}
	}
	return &e, nil
}

// parseLinks decodes the "links" key strictly.
func parseLinks(raw json.RawMessage) ([]Link, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("links must be a list of {name, service, port, path} objects")
	}
	var out []Link
	for i, it := range items {
		label := fmt.Sprintf("links[%d]", i)
		obj, err := objectKeys(it, label, "name", "service", "port", "path")
		if err != nil {
			return nil, err
		}
		var l Link
		for _, f := range []struct {
			key string
			dst interface{}
		}{{"name", &l.Name}, {"service", &l.Service}, {"port", &l.Port}, {"path", &l.Path}} {
			if err := field(obj, label, f.key, f.dst); err != nil {
				return nil, err
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// UpsertLink adds l, or replaces the link with the same name (any case) in
// place, keeping the recording order.
func (k *Knowledge) UpsertLink(l Link) {
	for i := range k.Links {
		if strings.EqualFold(k.Links[i].Name, l.Name) {
			k.Links[i] = l
			return
		}
	}
	k.Links = append(k.Links, l)
}

// RemoveLink drops the link named name (any case); it reports whether one was there.
func (k *Knowledge) RemoveLink(name string) bool {
	for i := range k.Links {
		if strings.EqualFold(k.Links[i].Name, name) {
			k.Links = append(k.Links[:i:i], k.Links[i+1:]...)
			return true
		}
	}
	return false
}

// entrySection is the "Entry and links" part of knowledge.md.
func (k *Knowledge) entrySection(b *strings.Builder) {
	if k.Entry == nil && len(k.Links) == 0 {
		return
	}
	b.WriteString("## Entry and links\n\n")
	if e := k.Entry; e != nil {
		switch {
		case e.None:
			b.WriteString("- App: none (no web entry)\n")
		case e.Proposed:
			fmt.Fprintf(b, "- App: %s:%d%s (proposed by the scan, not confirmed)\n", e.Service, e.Port, e.Path)
		default:
			fmt.Fprintf(b, "- App: %s:%d%s\n", e.Service, e.Port, e.Path)
		}
	}
	for _, l := range k.Links {
		if l.Service == "" && l.Port == 0 {
			fmt.Fprintf(b, "- %s: %s (on the entry)\n", l.Name, l.Path)
			continue
		}
		svc := l.Service
		if svc == "" {
			svc = "entry service"
		}
		fmt.Fprintf(b, "- %s: %s:%d%s\n", l.Name, svc, l.Port, l.Path)
	}
	b.WriteString("\n")
}
