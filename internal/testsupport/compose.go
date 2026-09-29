// Package testsupport holds small test-only helpers shared across internal
// packages. Never imported by non-test code.
package testsupport

import (
	"os/exec"
	"testing"
)

// RequireCompose skips the test cleanly when `docker compose` isn't
// installed and working - CI's macos-latest runner has no Docker at all,
// and every non-live test that shells out to `docker compose config`
// (Render, an Isolate round-trip, learn's Scan) must skip there rather than
// fail on an environment gap outside the test's own control. The one
// shared check, replacing what used to be a hasDocker(t) copy per package.
func RequireCompose(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
}
