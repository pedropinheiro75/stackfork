// Package secretstore reads and writes secret values in the operating
// system's credential store and in 1Password (design: add-secret-vault).
// A value never appears in an argument list, a log or an error.
package secretstore

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// Service is the credential-store service every keychain: item lives under.
const Service = "stackfork"

// Kind is the reference kind this package resolves.
type Kind string

const (
	KindKeychain Kind = "keychain"
	KindOP       Kind = "op"
)

var itemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// IsStoreRef reports whether ref has a keychain: or op:// prefix.
func IsStoreRef(ref string) bool {
	return strings.HasPrefix(ref, "keychain:") || strings.HasPrefix(ref, "op://")
}

// ValidItem reports whether item is a valid keychain item name.
func ValidItem(item string) bool { return itemPattern.MatchString(item) }

// ParseRef validates a keychain: or op:// reference (D1). For a keychain
// reference it returns the item name; for op:// the reference itself. The
// error never contains the input, which may be a value pasted by mistake.
func ParseRef(ref string) (Kind, string, error) {
	switch {
	case strings.HasPrefix(ref, "keychain:"):
		item := strings.TrimPrefix(ref, "keychain:")
		if !ValidItem(item) {
			return "", "", errors.New("keychain reference needs an item of 1 to 128 characters: letters, digits, '.', '_' or '-', starting with a letter or digit")
		}
		return KindKeychain, item, nil
	case strings.HasPrefix(ref, "op://"):
		segs := strings.Split(strings.TrimPrefix(ref, "op://"), "/")
		if len(segs) != 3 && len(segs) != 4 {
			return "", "", errors.New("op reference needs op://VAULT/ITEM/FIELD or op://VAULT/ITEM/SECTION/FIELD")
		}
		for _, s := range segs {
			if s == "" {
				return "", "", errors.New("op reference has an empty segment")
			}
			if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '?' }) {
				return "", "", errors.New("op reference segments cannot hold whitespace, control characters or '?' (no query strings)")
			}
		}
		return KindOP, ref, nil
	}
	return "", "", errors.New("not a keychain: or op:// reference")
}
