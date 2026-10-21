//go:build windows

package secretstore

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGetConsMode = kernel32.NewProc("GetConsoleMode")
	procSetConsMode = kernel32.NewProc("SetConsoleMode")
)

// WithoutEcho turns echo off on the console behind f (D10 on Windows) and
// returns the function that restores the previous mode. It fails when f is
// not a console.
func WithoutEcho(f *os.File) (restore func(), err error) {
	h := f.Fd()
	var mode uint32
	if r, _, _ := procGetConsMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return nil, errors.New("not a console")
	}
	if r, _, _ := procSetConsMode.Call(h, uintptr(consoleModeNoEcho(mode))); r == 0 {
		return nil, errors.New("cannot turn console echo off")
	}
	return func() { procSetConsMode.Call(h, uintptr(mode)) }, nil
}
