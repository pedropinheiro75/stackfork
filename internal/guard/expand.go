package guard

import (
	"strconv"
	"strings"
)

// maxGlobExpansions bounds expandGlob; a word that expands to more is
// treated as a hit (fail closed) rather than enumerated.
const maxGlobExpansions = 1024

// expandGlob expands shell braces ({a,b}, {a..c}, {1..3}, nested) and curl
// globs ([a-z], [0-9], [01-10], [abc], with an optional :step) the way bash
// and curl would, so an endpoint split across a glob is still seen. ok is
// false when the expansion exceeds limit.
func expandGlob(s string, limit int) (out []string, ok bool) {
	var rec func(done, todo string) bool
	rec = func(done, todo string) bool {
		i := strings.IndexAny(todo, "{[")
		if i < 0 {
			out = append(out, done+todo)
			return len(out) <= limit
		}
		alts, n, isGlob := globAlternatives(todo[i:], limit)
		if !isGlob {
			return rec(done+todo[:i+1], todo[i+1:])
		}
		for _, a := range alts {
			if !rec(done+todo[:i], a+todo[i+n:]) {
				return false
			}
		}
		if todo[i] == '{' && len(alts) == 1 {
			// a one-item {x}: bash leaves it as written
			return rec(done+todo[:i+n], todo[i+n:])
		}
		return true
	}
	ok = rec("", s)
	return out, ok
}

// globAlternatives reads the group at the start of s ("{" or "["): its
// alternatives and its length. isGlob is false for a literal bracket that
// neither bash nor curl would expand.
func globAlternatives(s string, limit int) (alts []string, n int, isGlob bool) {
	if s[0] == '[' {
		j := strings.IndexByte(s, ']')
		if j < 2 {
			return nil, 0, false
		}
		body := s[1:j]
		if k := strings.IndexByte(body, ':'); k >= 0 { // curl step: expand every value, a superset
			body = body[:k]
		}
		if lo, hi, isRange := strings.Cut(body, "-"); isRange && lo != "" && hi != "" {
			if r, ok := rangeOf(lo, hi, limit); ok {
				return r, j + 1, true
			}
		}
		for _, c := range strings.TrimLeft(body, "!^") {
			alts = append(alts, string(c))
		}
		return alts, j + 1, len(alts) > 0
	}
	depth, start := 0, 1
	var parts []string
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				parts = append(parts, s[start:j])
				if len(parts) > 1 {
					return parts, j + 1, true
				}
				if lo, hi, isSeq := strings.Cut(parts[0], ".."); isSeq {
					if r, ok := rangeOf(lo, hi, limit); ok {
						return r, j + 1, true
					}
				}
				// curl expands a one-item {x} to x; bash keeps it literal.
				// The literal form is checked too: see expandGlob.
				return []string{parts[0]}, j + 1, true
			}
		case ',':
			if depth == 1 {
				parts = append(parts, s[start:j])
				start = j + 1
			}
		}
	}
	return nil, 0, false
}

// rangeOf lists lo..hi for two integers (zero-padded like curl's [01-10])
// or two single characters, in either direction.
func rangeOf(lo, hi string, limit int) ([]string, bool) {
	var out []string
	if a, err1 := strconv.Atoi(lo); err1 == nil {
		b, err2 := strconv.Atoi(hi)
		if err2 != nil {
			return nil, false
		}
		width := 0
		if len(lo) > 1 && lo[0] == '0' {
			width = len(lo)
		}
		step := 1
		if b < a {
			step = -1
		}
		for v := a; ; v += step {
			s := strconv.Itoa(v)
			for len(s) < width {
				s = "0" + s
			}
			out = append(out, s)
			if v == b || len(out) > limit {
				break
			}
		}
		return out, true
	}
	if len(lo) != 1 || len(hi) != 1 {
		return nil, false
	}
	a, b := lo[0], hi[0]
	step := 1
	if b < a {
		step = -1
	}
	for c := int(a); ; c += step {
		out = append(out, string(rune(c)))
		if c == int(b) {
			break
		}
	}
	return out, true
}

// decodeANSIC replaces every bash $'...' word with its decoded text, so
// $'\x61pi' reads as api. Text outside those quotes is unchanged.
func decodeANSIC(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "$'")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i+2:]
		for len(s) > 0 && s[0] != '\'' {
			if s[0] != '\\' || len(s) == 1 {
				b.WriteByte(s[0])
				s = s[1:]
				continue
			}
			s = s[1:]
			c := s[0]
			s = s[1:]
			switch c {
			case 'a':
				b.WriteByte('\a')
			case 'b':
				b.WriteByte('\b')
			case 'e', 'E':
				b.WriteByte(0x1b)
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'v':
				b.WriteByte('\v')
			case '\\', '\'', '"', '?':
				b.WriteByte(c)
			case 'x', 'u', 'U':
				max := map[byte]int{'x': 2, 'u': 4, 'U': 8}[c]
				n := 0
				for n < max && n < len(s) && strings.IndexByte("0123456789abcdefABCDEF", s[n]) >= 0 {
					n++
				}
				if n == 0 {
					b.WriteByte('\\')
					b.WriteByte(c)
					continue
				}
				v, _ := strconv.ParseUint(s[:n], 16, 32)
				if c == 'x' {
					b.WriteByte(byte(v))
				} else {
					b.WriteRune(rune(v))
				}
				s = s[n:]
			case 'c':
				if len(s) > 0 {
					b.WriteByte(s[0] & 0x1f)
					s = s[1:]
				}
			default:
				if c >= '0' && c <= '7' {
					digits := string(c)
					for len(digits) < 3 && len(s) > 0 && s[0] >= '0' && s[0] <= '7' {
						digits += string(s[0])
						s = s[1:]
					}
					v, _ := strconv.ParseUint(digits, 8, 8)
					b.WriteByte(byte(v))
				} else {
					b.WriteByte('\\')
					b.WriteByte(c)
				}
			}
		}
		if len(s) > 0 {
			s = s[1:] // the closing quote
		}
	}
}
