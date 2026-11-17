package guard

import "strings"

// gatedEndpoints are the dashboard endpoints that run work or change state
// and sit behind a native confirmation. The dashboard routes them by exact
// path, so /api/run is one and /api/runs (a read) is not.
var gatedEndpoints = []string{"/api/run", "/api/actions", "/api/learn", "/api/secrets", "/api/live", "/api/env"}

// endpointReachable reports whether the text names a gated endpoint, or could
// expand to one under shell brace expansion, a curl glob or a shell
// expansion: /{api,x}/run, /ap[h-i]/run, /api/$X, /api/$(echo run).
//
// A literal endpoint counts anywhere in the text, with or without a prefix
// before it. A glob or an expansion counts only where a URL path starts,
// because the dashboard serves its endpoints at the root: /items/$id/run is
// an ordinary app call.
//
// The text is read as a list of atoms: literal characters, brace groups,
// bracket classes and expansions. An automaton searches the atoms for each
// endpoint, so the work is linear in the text whatever the text holds. It is a guardrail, so an atom it cannot read
// exactly (a nested group, a stepped sequence over words) counts as "could be
// anything without a slash". Brackets in a query, braces around a URL and an
// IPv6 host cannot form an endpoint, so they never match.
func endpointReachable(s string) bool {
	for _, ep := range gatedEndpoints {
		if literalEndpoint(s, ep) {
			return true
		}
	}
	if !strings.ContainsAny(s, "{[$`") {
		return false
	}
	atoms := tokenizeAtoms(s)
	for _, ep := range gatedEndpoints {
		if atomsReach(atoms, ep) {
			return true
		}
	}
	return false
}

// literalEndpoint reports whether s holds ep followed by the end of the text
// or a character that cannot continue a name: /api/run, /api/run?x and
// /api/run/x, not /api/runs.
func literalEndpoint(s, ep string) bool {
	for off := 0; ; {
		i := strings.Index(s[off:], ep)
		if i < 0 {
			return false
		}
		end := off + i + len(ep)
		if end == len(s) || !isNameByte(s[end]) {
			return true
		}
		off += i + 1
	}
}

type atomKind uint8

const (
	atomLit   atomKind = iota // lit holds a run of literal characters
	atomAlts                  // alts: one of these strings (brace group)
	atomClass                 // class: one character of a bracket class
	atomAny                   // any string without a slash (expansion)
)

type atom struct {
	kind atomKind
	lit  string
	alts []string
	neg  bool
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// tokenizeAtoms splits s into atoms in one left-to-right pass. A group or an
// expansion is consumed whole, so nested text is never read twice.
func tokenizeAtoms(s string) []atom {
	var atoms []atom
	var match []int32 // match[i] is the index of the } that closes the { at i, or -1
	if strings.IndexByte(s, '{') >= 0 {
		match = make([]int32, len(s))
		var stack []int32
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '{':
				match[i] = -1
				stack = append(stack, int32(i))
			case '}':
				if n := len(stack); n > 0 {
					match[stack[n-1]] = int32(i)
					stack = stack[:n-1]
				}
			}
		}
	}
	rb := -2 // cached index of the first ']' at or after the last search start; -1: none left
	litStart := 0
	flush := func(end int) {
		if end > litStart {
			atoms = append(atoms, atom{kind: atomLit, lit: s[litStart:end]})
		}
	}
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '{' && match[i] >= 0:
			flush(i)
			end := int(match[i])
			atoms = append(atoms, groupAtom(s[i+1:end]))
			i = end + 1
			litStart = i
		case c == '[':
			if rb == -2 || (rb >= 0 && rb < i) {
				if j := strings.IndexByte(s[i+1:], ']'); j >= 0 {
					rb = i + 1 + j
				} else {
					rb = -1
				}
			}
			if rb < 0 || rb == i+1 { // no closing bracket, or "[]": literal
				i++
				continue
			}
			flush(i)
			atoms = append(atoms, classAtom(s[i+1:rb]))
			i = rb + 1
			litStart = i
		case c == '$' && i+1 < len(s):
			end := expansionEnd(s, i, match)
			if end == i {
				i++
				continue
			}
			flush(i)
			atoms = append(atoms, atom{kind: atomAny})
			i = end
			litStart = i
		case c == '`':
			flush(i)
			atoms = append(atoms, atom{kind: atomAny})
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				i += j + 2
			} else {
				i = len(s)
			}
			litStart = i
		default:
			i++
		}
	}
	flush(len(s))
	return atoms
}

// expansionEnd is the index after the expansion that starts at s[i]=='$', or
// i when the $ is a plain character.
func expansionEnd(s string, i int, match []int32) int {
	switch c := s[i+1]; {
	case c == '(':
		depth := 0
		for j := i + 1; j < len(s); j++ {
			switch s[j] {
			case '(':
				depth++
			case ')':
				if depth--; depth == 0 {
					return j + 1
				}
			}
		}
		return len(s)
	case c == '{':
		if match != nil && match[i+1] >= 0 {
			return int(match[i+1]) + 1
		}
		return len(s)
	case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_':
		j := i + 1
		for j < len(s) && (isNameByte(s[j]) && s[j] != '-') {
			j++
		}
		return j
	case c >= '0' && c <= '9' || strings.IndexByte("@*#?!$-", c) >= 0:
		return i + 2
	}
	return i
}

// groupAtom reads the inside of a brace group. Alternatives with nested or
// expanding text, and sequences it cannot bound, become "any string".
func groupAtom(in string) atom {
	if strings.ContainsAny(in, "{}$`[") {
		return atom{kind: atomAny}
	}
	if !strings.Contains(in, ",") {
		// A bash sequence {a..z}, {u..z..2}, {1..9}: the characters from one
		// end to the other (the step only thins them out).
		if parts := strings.Split(in, ".."); len(parts) == 2 || len(parts) == 3 {
			if len(parts[0]) == 1 && len(parts[1]) == 1 {
				lo, hi := parts[0][0], parts[1][0]
				if lo > hi {
					lo, hi = hi, lo
				}
				return atom{kind: atomClass, lit: string([]byte{lo, '-', hi})}
			}
			return atom{kind: atomClass, lit: "0-9+-"} // numbers, signs and padding
		}
	}
	return atom{kind: atomAlts, alts: strings.Split(in, ",")}
}

// classAtom reads the inside of a bracket class.
func classAtom(in string) atom {
	if strings.ContainsAny(in, "[") {
		return atom{kind: atomAny}
	}
	a := atom{kind: atomClass}
	if in != "" && (in[0] == '!' || in[0] == '^') {
		a.neg, in = true, in[1:]
	}
	a.lit = in
	return a
}

func classHas(in string, neg bool, c byte) bool {
	in2 := in
	found := false
	for i := 0; i < len(in2); i++ {
		if i+2 < len(in2) && in2[i+1] == '-' {
			lo, hi := in2[i], in2[i+2]
			if lo > hi {
				lo, hi = hi, lo
			}
			if c >= lo && c <= hi {
				found = true
				break
			}
			i += 2
			continue
		}
		if in2[i] == c {
			found = true
			break
		}
	}
	return found != neg
}

// atomsReach reports whether the atoms can spell target followed by the end
// of the text or a character that cannot continue a path segment name
// (target "/api/run" matches "/api/run?x" and "/api/run/x", not "/api/runs").
// cur is the set of target prefixes matched at the current position, as bits.
// A match starts only where a URL path can start: at the beginning of the
// text, and at the first slash after a "://" or after the start.
func atomsReach(atoms []atom, target string) bool {
	n := len(target)
	cur := uint32(0)
	pending := false // the whole target was just matched; the next atom must end the name
	root := true     // a path may start here
	var p1, p2 byte  // the last two literal characters
	step := func(set uint32, c byte) (uint32, bool) {
		next, done := uint32(0), false
		for k := 0; k < n; k++ {
			if set&(1<<k) != 0 && target[k] == c {
				if k+1 == n {
					done = true
				} else {
					next |= 1 << (k + 1)
				}
			}
		}
		return next, done
	}
	for _, a := range atoms {
		if a.kind != atomLit && pending {
			return true
		}
		if root {
			cur |= 1
		}
		switch a.kind {
		case atomLit:
			for i := 0; i < len(a.lit); i++ {
				c := a.lit[i]
				if pending && !isNameByte(c) {
					return true
				}
				if root {
					cur |= 1
				}
				cur, pending = step(cur, c)
				if c == '/' {
					root = p1 == '/' && p2 == ':' // the // of a scheme: the host follows, then the path
				}
				p2, p1 = p1, c
			}
		case atomClass:
			next, done := uint32(0), false
			for k := 0; k < n; k++ {
				if cur&(1<<k) != 0 && classHas(a.lit, a.neg, target[k]) {
					if k+1 == n {
						done = true
					} else {
						next |= 1 << (k + 1)
					}
				}
			}
			cur, pending = next, done
		case atomAlts:
			next, done := uint32(0), false
			for k := 0; k < n; k++ {
				if cur&(1<<k) == 0 {
					continue
				}
				rest := n - k
				for _, alt := range a.alts {
					m := min(len(alt), rest)
					if alt[:m] != target[k:k+m] {
						continue
					}
					switch {
					case len(alt) < rest:
						next |= 1 << (k + len(alt))
					case len(alt) == rest:
						done = true
					case !isNameByte(alt[rest]):
						return true
					}
				}
			}
			cur, pending = next, done
		case atomAny:
			next, done := uint32(0), false
			for k := 0; k < n; k++ {
				if cur&(1<<k) == 0 {
					continue
				}
				next |= 1 << k
				for j := k; j < n && target[j] != '/'; j++ {
					if j+1 == n {
						done = true
					} else {
						next |= 1 << (j + 1)
					}
				}
			}
			cur, pending = next, done
		}
	}
	return pending
}
