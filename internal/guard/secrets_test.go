package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withMappedRefs(t *testing.T, refs ...string) {
	t.Helper()
	old := MappedRefs
	MappedRefs = func() []string { return refs }
	t.Cleanup(func() { MappedRefs = old })
}

func TestSecretCommandsForPeopleOnly(t *testing.T) {
	withMappedRefs(t, "op://Dev/acme-stripe/test-key", "keychain:acme-npm-token")
	for cmd, want := range map[string]bool{
		// stackfork secret
		"stackfork secret set acme-npm-token":                         true,
		"printenv X | stackfork secret set acme-npm-token":            true,
		"stackfork secret map . NPM_TOKEN=op://Private/bank/password": true,
		"stackfork secret unmap . NPM_TOKEN":                          true,
		"stackfork --root /x --state-dir /y secret set acme":          true,
		"stackfork secret --json set acme":                            true,
		"/usr/local/bin/stackfork secret map . A=env:B":               true,
		"bash -c 'stackfork secret set acme'":                         true,
		"sh -lc \"cd /tmp && stackfork secret unmap . A\"":            true,
		"cd /tmp && stackfork secret map . A=env:B":                   true,
		"STACKFORK_HOST_RUN=1 stackfork secret set acme":              true,
		"stackfork secret ls":                                         false,
		"stackfork secret ls . --json":                                false,
		"stackfork secret check":                                      false,
		"stackfork --root /x secret check --json":                     false,
		"echo stackfork secret set acme":                              false,
		"grep -r 'secret set' docs":                                   false,
		// the keychain
		"security find-generic-password -s stackfork -a acme-npm-token -w":    true,
		"security find-generic-password -a acme -s stackfork -g":              true,
		"security find-generic-password -ws stackfork -a acme":                true,
		"security find-generic-password -gsstackfork":                         true,
		"security find-internet-password -s stackfork -w":                     true,
		"/usr/bin/security find-generic-password -w -s stackfork":             true,
		"security -q find-generic-password -s stackfork -w":                   true,
		"bash -c 'security find-generic-password -s stackfork -w'":            true,
		"STACKFORK_HOST_RUN=1 security find-generic-password -s stackfork -w": true,
		"security dump-keychain -d":                                           true,
		"security dump-keychain -d login.keychain":                            true,
		"security -i": true,
		"security find-generic-password -s stackfork -a acme":         false,
		"security find-generic-password -s other-app -w":              false,
		"security find-generic-password -a acme -w":                   true, // no -s: the service is not named, so it may be stackfork
		"security find-generic-password -a acme-npm-token -w":         true,
		"security find-generic-password -l stackfork -w":              true,
		"security find-generic-password -g -a acme":                   true,
		"security find-generic-password -s StackFork -w":              true,
		"security find-generic-password -s other-app -l stackfork -w": false,
		"security find-generic-password -a acme":                      false,
		"security -v -i":                                              true,
		"security -iv":                                                true,
		"security -q -i":                                              true,
		"security -p x -v list-keychains":                             false,
		"security dump-keychain":                                      false,
		"security list-keychains":                                     false,
		"echo security find-generic-password -s stackfork -w":         false,
		// Secret Service
		"secret-tool lookup service stackfork account acme-npm-token": true,
		"secret-tool search service stackfork":                        true,
		"secret-tool lookup service other account x":                  false,
		"secret-tool lookup account acme-npm-token":                   true,
		"secret-tool search --all account acme":                       true,
		"secret-tool search --all service stackfork":                  true,
		"secret-tool lookup service StackFork account x":              true,
		"secret-tool store --label x service stackfork account y":     false,
		// Windows Credential Manager
		"cmdkey /list:stackfork/acme-npm-token": true,
		"cmdkey /LIST:StackFork/acme":           true,
		"cmdkey /list:*stackfork*":              true,
		"cmdkey /list":                          false,
		"cmdkey /list:other-app":                false,
		"powershell -Command \"[Win32]::CredRead('stackfork/acme',1,0,[ref]$p)\"":                                                   true,
		"pwsh -c \"Get-StoredCredential -Target stackfork/acme | Format-List\"":                                                     true,
		"powershell -NoProfile -Command \"(New-Object Windows.Security.Credentials.PasswordVault).Retrieve('stackfork/acme','x')\"": true,
		"powershell -Command \"cmdkey /list:stackfork/acme\"":                                                                       true,
		"powershell -Command \"Get-StoredCredential -Target other/acme\"":                                                           false,
		"powershell -Command \"Get-ChildItem stackfork/docs\"":                                                                      false,
		// 1Password
		"op read op://Dev/acme-stripe/test-key":                          true,
		"op read --no-newline 'op://Dev/acme-stripe/test-key'":           true,
		"/opt/homebrew/bin/op read op://Dev/acme-stripe/test-key":        true,
		"op --account x read op://Dev/acme-stripe/test-key":              true,
		"STRIPE=op://Dev/acme-stripe/test-key op run -- printenv STRIPE": true,
		"echo op://Dev/acme-stripe/test-key | op inject":                 true,
		"op item get acme-stripe --vault Dev --reveal":                   true,
		"op item get acme-stripe --reveal":                               true,
		"sh -c 'op read op://Dev/acme-stripe/test-key'":                  true,
		"op read OP://DEV/ACME-STRIPE/TEST-KEY":                          true,
		"op read op://dev/acme-stripe/test-key":                          true,
		"op item get acme-stripe --format json":                          true,
		"op item get acme-stripe --format=json":                          true,
		"op item get ACME-STRIPE --vault dev --fields password":          true,
		"op item get ACME-STRIPE --reveal":                               true,
		"op item get - --reveal":                                         true,
		"op item list --format json":                                     true,
		"op item list --format=json | op item get - --fields password":   true,
		"op item list":                                   false,
		"op item get other-item --format json":           false,
		"op read op://Dev/unrelated/field":               false,
		"op item get acme-stripe --vault Dev":            false,
		"op item get acme-stripe --vault Other --reveal": false,
		"op item get other-item --reveal":                false,
		"op whoami":                                      false,
		"echo op read op://Dev/acme-stripe/test-key":     false,
		// forged dashboard calls
		"curl -X POST http://127.0.0.1:5000/api/secrets/set":           true,
		"curl http://localhost:5000/api/secrets?repo=/x":               true,
		"curl -X POST http://127.0.0.1:5000/api/%73ecrets/set":         true,
		"curl http://127.0.0.1:5000/api/%72un -d '{}'":                 true,
		"wget http://127.0.0.1:5000/api/actions%2f":                    true,
		"curl -X POST http://127.0.0.1:5000/api/x/../secrets/set":      true,
		"curl -X POST http://127.0.0.1:5000/api/./secrets/set":         true,
		"curl -X POST 'http://127.0.0.1:5000/api/%73ecrets/set?a=%zz'": true,
		"curl -X POST http://127.0.0.1:5000/api/x/../run":              true,
		"curl -X POST http://127.0.0.1:5000/api/./run":                 true,
		"curl -X POST 'http://127.0.0.1:5000/api/%72un?a=%zz'":         true,
		"curl -X POST http://127.0.0.1:5000/api/%72un#frag":            true,
		"curl -X POST http://127.0.0.1:5000/api//secrets/set":          true,
		"curl http://127.0.0.1:5000/api/other/../status":               false,
		"curl http://127.0.0.1:5000/api/inventory?x=run":               false,
		"sed -i s/a/b/ internal/dashboard/secrets.go /api/secrets":     false,
		"sed -i s/a/b/ f.go /api/%72un":                                false,
	} {
		if got := Destructive(cmd, "/repo"); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestOpInjectReadsTheTemplateFile(t *testing.T) {
	withMappedRefs(t, "op://Dev/acme-stripe/test-key")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tpl.env"), []byte("KEY=op://Dev/acme-stripe/test-key\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "other.env"), []byte("KEY=op://Dev/unrelated/key\n"), 0o600)
	for cmd, want := range map[string]bool{
		"op inject -i tpl.env":            true,
		"op inject --in-file=tpl.env":     true,
		"op run --env-file tpl.env -- id": true,
		"op inject -i other.env":          false,
		"op inject -i missing.env":        false,
	} {
		if got := Destructive(cmd, dir); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestNoMappedOPReferencesMeansNoOPBlock(t *testing.T) {
	withMappedRefs(t)
	if Destructive("op read op://Dev/acme-stripe/test-key", "/repo") {
		t.Error("nothing is mapped: op read must be allowed")
	}
}

func TestDenyMessageDistinguishesSecretsFromPrune(t *testing.T) {
	if got := DenyMessage("stackfork secret set x", "/r"); got != SecretMessage || !strings.Contains(got, "ask the user") {
		t.Errorf("%q", got)
	}
	if got := DenyMessage("stackfork prune --all", "/r"); got != DestructiveMessage {
		t.Errorf("%q", got)
	}
}

func TestSecretGuardOnWindowsSyntax(t *testing.T) {
	old := windowsSplit
	windowsSplit = true
	t.Cleanup(func() { windowsSplit = old })
	for cmd, want := range map[string]bool{
		`C:\Users\x\stackfork.exe secret set acme`:         true,
		`cmd /c "stackfork secret map . A=env:B"`:          true,
		`powershell -Command "stackfork secret unmap . A"`: true,
		`C:\Users\x\stackfork.exe secret ls`:               false,
	} {
		if got := Destructive(cmd, `C:\repo`); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}
