package guard

import "testing"

func TestLiveFormsAreForPeopleOnly(t *testing.T) {
	for cmd, want := range map[string]bool{
		"stackfork live on":                                    true,
		"stackfork live on acme-web":                           true,
		"stackfork --root /x live on":                          true,
		"stackfork --state-dir /s --secret A=env:B live on":    true,
		"stackfork live --root /x on":                          true,
		"stackfork --instance sf-1 live on":                    true,
		"/usr/local/bin/stackfork live on":                     true,
		"/usr/local/bin/stackfork.exe live on":                 true,
		"bash -c 'stackfork live on'":                          true,
		"sh -lc \"cd /x && stackfork --root=/x live on\"":      true,
		"env FOO=1 stackfork live on":                          true,
		"sudo -u root stackfork live on":                       true,
		"cd /tmp && stackfork live on .":                       true,
		"stackfork live cleanup auto":                          true,
		"stackfork live cleanup auto acme-web --grace 24":      true,
		"stackfork live cleanup suggest --delete-branch":       true,
		"stackfork live cleanup suggest --delete-branch=true":  true,
		"stackfork live cleanup suggest -delete-branch":        true,
		"stackfork --root /x live cleanup auto":                true,
		"curl -X POST http://127.0.0.1:26000/api/live":         true,
		"stackfork live clean acme-web feature-login --yes":    true,
		"stackfork live clean feature-login --yes":             true,
		"stackfork live clean --yes feature-login":             true,
		"stackfork live clean feature-login -yes":              true,
		"stackfork live clean feature-login --yes=true":        true,
		"stackfork --root /x live clean feature-login --yes":   true,
		"bash -c 'stackfork live clean x --yes'":               true,
		"cd /tmp && stackfork live clean acme-web x --yes":     true,
		"/usr/local/bin/stackfork.exe live clean x --yes":      true,
		"stackfork live clean acme-web feature-login":          false,
		"stackfork live clean feature-login":                   false,
		"stackfork --root /x live clean feature-login":         false,
		"stackfork live status":                                false,
		"stackfork live status --json":                         false,
		"stackfork live status auto":                           false,
		"stackfork live off":                                   false,
		"stackfork live off acme-web":                          false,
		"stackfork live cleanup off":                           false,
		"stackfork live cleanup suggest":                       false,
		"stackfork live cleanup suggest --keep-branch":         false,
		"stackfork --root /x live status":                      false,
		"echo stackfork live on":                               false,
		"grep -r 'live on' docs":                               false,
		"stackfork learn record live-watch x --action rebuild": false,
		"stackfork help live on":                               false,
	} {
		if got := Destructive(cmd, "/repo"); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestLiveMessageForLiveForms(t *testing.T) {
	if DenyMessage("stackfork live on", "/repo") != LiveMessage {
		t.Error("live on must get the live message")
	}
	if DenyMessage("stackfork gc --apply", "/repo") != DestructiveMessage {
		t.Error("other forms keep the destructive message")
	}
}

func TestLiveOnWindowsSyntax(t *testing.T) {
	old := windowsSplit
	windowsSplit = true
	t.Cleanup(func() { windowsSplit = old })
	for cmd, want := range map[string]bool{
		`C:\Users\x\stackfork.exe live on`:                  true,
		`cmd /c "stackfork live on"`:                        true,
		`powershell -Command "stackfork live cleanup auto"`: true,
		`C:\Users\x\stackfork.exe live status`:              false,
	} {
		if got := Destructive(cmd, `C:\repo`); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}
