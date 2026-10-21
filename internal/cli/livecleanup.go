package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// cleanupDown is the `down` step of a branch cleanup (add-live-mode D19):
// `stackfork --instance P down --volumes` in this process. It runs after the
// worktree was removed, so the checkout comes from the instance's manifest,
// as in the orphan cleanup of `down --instance`. It stops host apps and
// containers and deletes the instance's volumes, database included.
func cleanupDown(newBase, legacyBase string) func(live.Target) func(io.Writer) error {
	return func(t live.Target) func(io.Writer) error {
		return func(w io.Writer) error {
			root, branch, instanceDir, err := resolveInstance(t.Project, newBase, legacyBase)
			if err != nil {
				return err
			}
			lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
			if err != nil {
				return err
			}
			defer lock.Release()
			fmt.Fprintf(w, "removing %s with its volumes\n", t.Project)
			code, err := lifecycle.Execute(w, w, "down", root, branch, t.Project, instanceDir, newBase, legacyBase, lifecycle.Options{Volumes: true})
			if err == nil && code != 0 {
				err = fmt.Errorf("down exited with status %d", code)
			}
			return err
		}
	}
}
