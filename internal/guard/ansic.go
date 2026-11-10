package guard

import (
	"strconv"
	"strings"
)

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
					v, _ := strconv.ParseUint(digits, 8, 16)
					b.WriteByte(byte(v)) // bash wraps: \565 is 0x175, written as 'u'
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
