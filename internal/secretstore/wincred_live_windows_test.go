//go:build windows

package secretstore

import (
	"syscall"
	"testing"
	"unsafe"
)

// Round trip against the real Credential Manager with a throwaway
// stackfork/stackfork-test-<random> entry. Opt-in like the other live tests.
func TestLiveCredentialManagerRoundTrip(t *testing.T) {
	if !liveEnabled("STACKFORK_LIVE_KEYCHAIN") {
		t.Skip("set STACKFORK_LIVE_KEYCHAIN=1 to touch the Credential Manager with a throwaway item")
	}
	item := throwawayItem(t)
	target, _ := syscall.UTF16PtrFromString(credTarget(item))
	del := advapi32.NewProc("CredDeleteW")
	defer del.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0)

	b := &Backend{}
	ref := "keychain:" + item
	value := "live-" + item + `-\'"$ x çã€😀`
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
	if err := b.Store(item, value+"2"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := b.Resolve(ref); got != value+"2" {
		t.Fatal("update did not replace the value")
	}
}
