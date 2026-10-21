//go:build !windows

package secretstore

import (
	"errors"
	"os"
)

// WithoutEcho is the Windows console echo switch; Unix uses stty.
func WithoutEcho(*os.File) (func(), error) { return nil, errors.New("only on Windows") }
