//go:build windows

package cleanup

import "os"

// ownerOK: Windows has no owner check here; the fixed paths are Unix paths,
// so gh is never found.
func ownerOK(os.FileInfo) bool { return true }
