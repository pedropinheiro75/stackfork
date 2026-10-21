package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// startRefusal is the readiness check of StartEnv: same error text, no side effect.
func TestStartRefusalIsWhatStartEnvReturns(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	root := scratchRepo(t, filepath.Join(t.TempDir(), "acme-web"))
	os.MkdirAll(filepath.Join(root, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(root, ".stackfork", "dev.compose.yaml"), []byte("services: {}\n"), 0o644)
	newBase, legacy := t.TempDir(), t.TempDir()
	root, _ = identity.ResolvePath(root)
	branch, _ := currentBranch(root)
	_, inst, err := checkoutInstance(root, branch, newBase, legacy)
	if err != nil {
		t.Fatal(err)
	}
	_, refusal := startRefusal(root, root, branch, inst, []inventory.Instance(nil), nil, false)
	if refusal == nil {
		t.Fatal("an adapter alone is not verified knowledge: expected a refusal")
	}
	got := StartEnv(io.Discard, io.Discard, StartOptions{Checkout: root, NewStateBase: newBase, LegacyStateBase: legacy})
	if got == nil || got.Error() != refusal.Error() {
		t.Errorf("StartEnv: %v\nstartRefusal: %v", got, refusal)
	}
	if warn, err := startRefusal(root, root, branch, inst, nil, nil, true); err != nil || warn == "" {
		t.Errorf("--force warns instead: %q %v", warn, err)
	}
	// a prepared instance is never refused
	os.MkdirAll(inst, 0o755)
	os.WriteFile(filepath.Join(inst, "manifest.json"), []byte("{}"), 0o600)
	if warn, err := startRefusal(root, root, branch, inst, nil, nil, false); err != nil || warn != "" {
		t.Errorf("%q %v", warn, err)
	}
}
