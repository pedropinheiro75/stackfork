//go:build windows

package secretstore

import (
	"syscall"
	"unsafe"
)

var (
	advapi32     = syscall.NewLazyDLL("advapi32.dll")
	procCredRead = advapi32.NewProc("CredReadW")
	procCredWrit = advapi32.NewProc("CredWriteW")
	procCredFree = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric        = 1 // CRED_TYPE_GENERIC
	credPersistLocalMachin = 2 // CRED_PERSIST_LOCAL_MACHINE
)

// credentialW is CREDENTIALW.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// osCredRead returns a copy of the blob; the API's own buffer is zeroed
// and freed before returning.
func osCredRead(target string) ([]byte, uintptr) {
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return nil, uintptr(syscall.EINVAL)
	}
	var p *credentialW
	r, _, e := procCredRead.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&p)))
	if r == 0 {
		return nil, errno(e)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(p)))
	n := int(p.CredentialBlobSize)
	if n == 0 || p.CredentialBlob == nil {
		return nil, 0
	}
	src := unsafe.Slice(p.CredentialBlob, n)
	out := append([]byte(nil), src...)
	zero(src)
	return out, 0
}

func osCredWrite(target string, blob []byte) uintptr {
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return uintptr(syscall.EINVAL)
	}
	user, _ := syscall.UTF16PtrFromString(Service)
	c := credentialW{
		Type:               credTypeGeneric,
		TargetName:         t,
		CredentialBlobSize: uint32(len(blob)),
		Persist:            credPersistLocalMachin,
		UserName:           user,
	}
	if len(blob) > 0 {
		c.CredentialBlob = &blob[0]
	}
	r, _, e := procCredWrit.Call(uintptr(unsafe.Pointer(&c)), 0)
	if r == 0 {
		return errno(e)
	}
	return 0
}

func errno(e error) uintptr {
	if n, ok := e.(syscall.Errno); ok && n != 0 {
		return uintptr(n)
	}
	return 1 // generic failure
}
