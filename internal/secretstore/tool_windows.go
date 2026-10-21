//go:build windows

package secretstore

import (
	"os"
	"syscall"
	"unsafe"
)

var procGetNamedSecurityInfo = advapi32.NewProc("GetNamedSecurityInfoW")

const (
	seFileObject      = 1 // SE_FILE_OBJECT
	ownerSecurityInfo = 1 // OWNER_SECURITY_INFORMATION
)

// ownerOK: the owner of path is the current user, SYSTEM, Administrators or
// TrustedInstaller (D2). Any failure to read it refuses the file.
func ownerOK(path string, _ os.FileInfo) bool {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return false
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	var owner *syscall.SID
	var sd uintptr
	r, _, _ := procGetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(p)), seFileObject, ownerSecurityInfo,
		uintptr(unsafe.Pointer(&owner)), 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if r != 0 || owner == nil {
		return false
	}
	defer syscall.LocalFree(syscall.Handle(sd))
	s, err := owner.String()
	if err != nil {
		return false
	}
	cur, err := u.User.Sid.String()
	if err != nil {
		return false
	}
	return ownerSIDAllowed(s, cur)
}
