package secretstore

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// Opt-in live round trips against the real credential store of this machine.
// Off by default; each uses a throwaway item (service stackfork, item
// stackfork-test-<random>) that the test deletes. Values are never logged.

func throwawayItem(t *testing.T) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "stackfork-test-" + hex.EncodeToString(b)
}

func TestLiveKeychainRoundTrip(t *testing.T) {
	if os.Getenv("STACKFORK_LIVE_KEYCHAIN") != "1" {
		t.Skip("set STACKFORK_LIVE_KEYCHAIN=1 to touch the real credential store with a throwaway item")
	}
	item := throwawayItem(t)
	ref := "keychain:" + item
	value := "live-" + throwawayItem(t) + `-\'"$ x`
	b := &Backend{}
	switch runtime.GOOS {
	case "darwin":
		defer exec.Command("/usr/bin/security", "delete-generic-password", "-s", Service, "-a", item).Run()
	case "linux":
		defer exec.Command("/usr/bin/secret-tool", "clear", "service", Service, "account", item).Run()
	default:
		t.Skip("no backend on " + runtime.GOOS)
	}
	if st := b.Exists(ref); st != Missing {
		t.Fatalf("before store: %v", st)
	}
	if err := b.Store(item, value); err != nil {
		t.Fatal(err)
	}
	got, st, err := b.Resolve(ref)
	if st != Resolves || err != nil || got != value {
		t.Fatalf("round trip: status %v err %v equal=%v", st, err, got == value)
	}
	if st := b.Exists(ref); st != Resolves {
		t.Fatalf("after store: %v", st)
	}
	if err := b.Store(item, value+"2"); err != nil { // -U updates
		t.Fatal(err)
	}
	if got, _, _ := b.Resolve(ref); got != value+"2" {
		t.Fatal("update did not replace the value")
	}
}
