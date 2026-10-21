package secretstore

import (
	"errors"
	"fmt"
	"unicode/utf16"
)

// Windows Credential Manager (design D3, slice 2). The logic lives here so
// every OS tests it; wincred_windows.go only makes the advapi32 calls.

// credTargetPrefix keeps every stackfork item under one prefix, so only
// stackfork's own entries are reachable through this package.
const credTargetPrefix = "stackfork/"

// credMaxBlob is CRED_MAX_CREDENTIAL_BLOB_SIZE (5 * 512 bytes).
const credMaxBlob = 5 * 512

const (
	errNotFound     = 1168 // ERROR_NOT_FOUND
	errNoLogon      = 1312 // ERROR_NO_SUCH_LOGON_SESSION
	errCancelled    = 1223 // ERROR_CANCELLED
	errNotImplement = 120  // ERROR_CALL_NOT_IMPLEMENTED
)

func credTarget(item string) string { return credTargetPrefix + item }

// credBlob encodes value as UTF-16LE (what cmdkey and the Credential Manager
// UI use). The error never contains the value.
func credBlob(value string) ([]byte, error) {
	u := utf16.Encode([]rune(value))
	if len(u)*2 > credMaxBlob {
		return nil, fmt.Errorf("the Windows Credential Manager holds at most %d characters per value", credMaxBlob/2)
	}
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return b, nil
}

func decodeBlob(b []byte) (string, error) {
	if len(b)%2 != 0 {
		return "", errors.New("the stored value is not UTF-16")
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	s := string(utf16.Decode(u))
	for i := range u {
		u[i] = 0
	}
	return s, nil
}

// credStatus maps a Win32 error code (0 = success) to a Status (D4).
func credStatus(code uintptr) Status {
	switch code {
	case 0:
		return Resolves
	case errNotFound:
		return Missing
	case errNoLogon, errNotImplement:
		return Unavailable
	case errCancelled:
		return Locked
	}
	return Error
}

// The OS calls, replaceable by tests. They return the Win32 error code.
var (
	credRead  = osCredRead
	credWrite = osCredWrite
)

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func (b *Backend) winRead(item string, wantValue bool) (string, Status, error) {
	blob, code := credRead(credTarget(item))
	defer zero(blob)
	if st := credStatus(code); st != Resolves {
		return "", st, fmt.Errorf("Windows Credential Manager: %s (code %d)", credMessage(st, item), code)
	}
	if len(blob) == 0 {
		return "", Missing, fmt.Errorf("credential %s is empty", item)
	}
	if !wantValue {
		return "", Resolves, nil
	}
	v, err := decodeBlob(blob)
	if err != nil {
		return "", Error, err
	}
	return v, Resolves, nil
}

func (b *Backend) winStore(item, value string) error {
	blob, err := credBlob(value)
	if err != nil {
		return err
	}
	defer zero(blob)
	if code := credWrite(credTarget(item), blob); code != 0 {
		return fmt.Errorf("Windows Credential Manager: %s (code %d)", credMessage(credStatus(code), item), code)
	}
	return nil
}

func credMessage(st Status, item string) string {
	switch st {
	case Missing:
		return "credential " + item + " not found"
	case Unavailable:
		return "not available here"
	case Locked:
		return "the request was cancelled"
	}
	return "the call failed"
}
