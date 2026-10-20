//go:build windows

package identity

import (
	"os"
	"strings"
	"testing"
)

func TestResolvePathWindows(t *testing.T) {
	got, err := ResolvePath(`C:\x\..\y`)
	if err != nil || got != `C:\y` {
		t.Fatalf("got %q, %v; want C:\\y", got, err)
	}
	got, err = ResolvePath(`\\?\C:\y`)
	if err != nil || got != `C:\y` {
		t.Fatalf("extended: got %q, %v; want C:\\y", got, err)
	}
}

func TestStateDirUnderLocalAppData(t *testing.T) {
	dir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, os.Getenv("LOCALAPPDATA")) {
		t.Fatalf("%q is not under LOCALAPPDATA %q", dir, os.Getenv("LOCALAPPDATA"))
	}
}
