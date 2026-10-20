//go:build windows

package secretstore

import "os"

// ownerOK: Windows is a stub in slice 1 (every call is unavailable before a
// tool is resolved); slice 2 checks the owner SID.
func ownerOK(os.FileInfo) bool { return false }
