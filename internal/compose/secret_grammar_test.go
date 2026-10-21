package compose

import (
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/secretstore"
)

// Task 1.1: keychain: and op:// references share one validator for flags,
// the machine map and the manifest.
func TestValidateSecretRefKinds(t *testing.T) {
	long := strings.Repeat("a", 128)
	for _, ok := range []string{
		"env:VAR",
		"npmrc:/p/.npmrc#registry.example.com",
		"keychain:acme-npm-token",
		"keychain:a",
		"keychain:" + long,
		"keychain:A.b_c-1",
		"op://Dev/acme-stripe/test-key",
		"op://Dev/acme-stripe/section/test-key",
	} {
		if err := ValidateSecretRef(ok); err != nil {
			t.Errorf("refused valid %q: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"keychain:",
		"keychain:-lead",
		"keychain:.lead",
		"keychain:" + long + "a",
		"keychain:has space",
		"keychain:a/b",
		"keychain:tab\there",
		"keychain:nul\x00x",
		"keychain:ünï",
		"op://",
		"op://Dev",
		"op://Dev/item",
		"op://Dev//field",
		"op:///item/field",
		"op://Dev/item/field/",
		"op://Dev/item/a/b/c",
		"op://Dev/item/field?attribute=otp",
		"op://Dev/it em/field",
		"op://Dev/item/fi\neld",
		"op://Dev/item/fi\x1beld",
		"op:/Dev/item/field",
		"OP://Dev/item/field",
		"ghp_canaryRawTokenValue",
	} {
		err := ValidateSecretRef(bad)
		if err == nil {
			t.Errorf("accepted %q", bad)
			continue
		}
		if (len(bad) > 5 && strings.Contains(err.Error(), bad)) || strings.Contains(err.Error(), "canaryRawToken") {
			t.Errorf("error echoes the input %q: %v", bad, err)
		}
	}
}

// Task 1.1: an error never contains input that does not parse as a
// reference (it may be a pasted value) and names the secret only when the
// name is itself a valid name.
func TestParseSecretFlagNeverEchoesUnparsedInput(t *testing.T) {
	for _, raw := range []string{
		"ghp_canaryRawTokenValue",
		"NPM_TOKEN=ghp_canaryRawTokenValue",
		"NPM_TOKEN=keychain:ghp canaryRawTokenValue",
		"NPM_TOKEN=op://Dev/x/y?canaryRawTokenValue",
		"=canaryRawTokenValue",
		"canary RawToken=env:X",
		"abc==canaryRawTokenValue",
	} {
		_, _, err := ParseSecretFlag(raw)
		if err == nil {
			t.Errorf("accepted %q", raw)
			continue
		}
		if strings.Contains(err.Error(), "canary") {
			t.Errorf("error echoes input for %q: %v", raw, err)
		}
	}
	_, _, err := ParseSecretFlag("NPM_TOKEN=keychain:bad item")
	if err == nil || !strings.Contains(err.Error(), "NPM_TOKEN") {
		t.Fatalf("error must name the secret: %v", err)
	}
	name, ref, err := ParseSecretFlag("NPM_TOKEN=keychain:acme-npm-token")
	if err != nil || name != "NPM_TOKEN" || ref != "keychain:acme-npm-token" {
		t.Fatalf("got %q %q %v", name, ref, err)
	}
	if _, ref, err = ParseSecretFlag("STRIPE=op://Dev/acme-stripe/test-key"); err != nil || ref != "op://Dev/acme-stripe/test-key" {
		t.Fatalf("op ref: %q %v", ref, err)
	}
}

// Task 1.2: keychain: and op:// resolve through secretstore; a status other
// than resolves becomes an error naming the secret and the reference.
func TestResolveSecretDelegatesToSecretstore(t *testing.T) {
	old := secretstore.Default
	secretstore.Default = &secretstore.Backend{GOOS: "windows"}
	defer func() { secretstore.Default = old }()
	for _, ref := range []string{"keychain:acme-npm-token", "op://Dev/acme-stripe/test-key"} {
		v, err := ResolveSecret("NPM_TOKEN", ref)
		if v != "" || err == nil ||
			!strings.Contains(err.Error(), "NPM_TOKEN") || !strings.Contains(err.Error(), ref) ||
			!strings.Contains(err.Error(), "unavailable") {
			t.Errorf("%s: %q %v", ref, v, err)
		}
	}
}

// Review I2: a command's --secret takes only env: and npmrc:. keychain: and
// op:// come from the machine map, which only a person writes.
func TestParseCommandSecretFlagRefusesStoreReferences(t *testing.T) {
	for _, raw := range []string{"NPM_TOKEN=env:NPM_TOKEN", "NPM_TOKEN=npmrc:/home/u/.npmrc#npm.pkg.github.com"} {
		if _, _, err := ParseCommandSecretFlag(raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"NPM_TOKEN=keychain:other-item", "NPM_TOKEN=op://Private/bank/password", "NPM_TOKEN=keychain:bad item"} {
		_, _, err := ParseCommandSecretFlag(raw)
		if err == nil {
			t.Errorf("%s: accepted", raw)
			continue
		}
		for _, bad := range []string{"other-item", "bank", "bad item"} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("%s: the error echoes the reference: %v", raw, err)
			}
		}
		if !strings.Contains(err.Error(), "stackfork secret map") {
			t.Errorf("%s: the error must point to secret map: %v", raw, err)
		}
	}
}
