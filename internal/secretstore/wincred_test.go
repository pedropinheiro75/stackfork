package secretstore

import (
	"runtime"
	"strings"
	"testing"
)

func TestCredTarget(t *testing.T) {
	if got := credTarget("acme-npm-token"); got != "stackfork/acme-npm-token" {
		t.Fatal(got)
	}
}

func TestCredBlobRoundTrip(t *testing.T) {
	for _, v := range []string{"abc", `p@ss \'"$ w0rd`, "çãõ-€", "😀emoji"} {
		b, err := credBlob(v)
		if err != nil {
			t.Fatal(err)
		}
		if len(b)%2 != 0 {
			t.Fatalf("%q: odd blob length", v)
		}
		got, err := decodeBlob(b)
		if err != nil || got != v {
			t.Fatalf("%q: got %q err %v", v, got, err)
		}
	}
	if b, _ := credBlob("A"); len(b) != 2 || b[0] != 'A' || b[1] != 0 {
		t.Fatalf("not UTF-16LE: %v", b)
	}
}

func TestCredBlobSizeLimit(t *testing.T) {
	if _, err := credBlob(strings.Repeat("x", credMaxBlob/2)); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	// a surrogate pair takes 4 bytes
	_, err := credBlob(strings.Repeat("x", credMaxBlob/2-1) + "😀")
	if err == nil || strings.Contains(err.Error(), "xxx") {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := credBlob(strings.Repeat("x", credMaxBlob/2+1)); err == nil {
		t.Fatal("one character over the limit was accepted")
	}
}

func TestDecodeBlobOdd(t *testing.T) {
	if _, err := decodeBlob([]byte{1, 2, 3}); err == nil {
		t.Fatal("odd length accepted")
	}
}

func TestCredStatus(t *testing.T) {
	for code, want := range map[uintptr]Status{
		0:    Resolves,
		1168: Missing,     // ERROR_NOT_FOUND
		1312: Unavailable, // ERROR_NO_SUCH_LOGON_SESSION
		120:  Unavailable, // ERROR_CALL_NOT_IMPLEMENTED (build without the API)
		1223: Locked,      // ERROR_CANCELLED
		5:    Error,
	} {
		if got := credStatus(code); got != want {
			t.Errorf("code %d: %v, want %v", code, got, want)
		}
	}
}

// fakeCred swaps the OS calls for an in-memory store.
func fakeCred(t *testing.T) map[string][]byte {
	m := map[string][]byte{}
	oldR, oldW := credRead, credWrite
	t.Cleanup(func() { credRead, credWrite = oldR, oldW })
	credRead = func(target string) ([]byte, uintptr) {
		b, ok := m[target]
		if !ok {
			return nil, 1168
		}
		return append([]byte(nil), b...), 0
	}
	credWrite = func(target string, blob []byte) uintptr {
		m[target] = append([]byte(nil), blob...)
		return 0
	}
	return m
}

func TestWindowsBackendThroughCredentialManager(t *testing.T) {
	m := fakeCred(t)
	b := &Backend{GOOS: "windows"}
	ref := "keychain:acme-npm-token"
	if _, st, err := b.Resolve(ref); st != Missing || err == nil {
		t.Fatalf("before: %v %v", st, err)
	}
	if st := b.Exists(ref); st != Missing {
		t.Fatal(st)
	}
	if err := b.Store("acme-npm-token", `s3cr3t \'"`); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["stackfork/acme-npm-token"]; !ok {
		t.Fatalf("target: %v", m)
	}
	v, st, err := b.Resolve(ref)
	if v != `s3cr3t \'"` || st != Resolves || err != nil {
		t.Fatalf("%q %v %v", v, st, err)
	}
	if st := b.Exists(ref); st != Resolves {
		t.Fatal(st)
	}
	if err := b.Store("acme-npm-token", strings.Repeat("x", credMaxBlob)); err == nil || strings.Contains(err.Error(), "xxxx") {
		t.Fatalf("too large: %v", err)
	}
	m["stackfork/empty"] = nil
	if _, st, _ := b.Resolve("keychain:empty"); st != Missing {
		t.Fatalf("empty blob: %v", st)
	}
}

func TestWindowsBuildWithoutAPIIsUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("checks the stub of non-Windows builds; here the real Credential Manager answers")
	}
	b := &Backend{GOOS: "windows"} // on this OS the real calls are the stub
	if _, st, err := b.Resolve("keychain:acme-npm-token"); st != Unavailable || err == nil {
		t.Fatalf("%v %v", st, err)
	}
}
