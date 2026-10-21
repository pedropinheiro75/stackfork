//go:build windows

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// Written for a future Windows runner; not run on a Windows machine yet. A
// host exec step whose program is a .cmd file is refused before it starts
// when an argument holds a cmd.exe metacharacter, and nothing runs.
func TestHostExecRefusesABatchFileArgumentOnWindows(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ran.txt")
	if err := os.WriteFile(filepath.Join(root, "migrate.cmd"), []byte("@echo off\r\necho ran > \""+marker+"\"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	step := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Cmd: "migrate.cmd up%PATH%"}
	_, err := runHostExec(&out, "acme-web", root, filepath.Join(root, "inst"), root, root, step)
	if err == nil || !strings.Contains(err.Error(), "cmd.exe") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the batch file ran")
	}
}
