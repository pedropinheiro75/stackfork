//go:build !windows

package secretstore

func osCredRead(string) ([]byte, uintptr) { return nil, errNotImplement }
func osCredWrite(string, []byte) uintptr  { return errNotImplement }
