//go:build !darwin

package identity

// trueCase is a no-op off macOS.
// ponytail: Windows volumes are case-insensitive too; GetFinalPathNameByHandle if a case mismatch shows up there.
func trueCase(p string) string { return p }
