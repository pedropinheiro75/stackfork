package redact

import "testing"

func TestSecretLikeName(t *testing.T) {
	for name, want := range map[string]bool{
		"API_TOKEN": true, "db_password": true, "MY_SECRET": true, "X_PASSWD": true, "AWS_KEY": true,
		"SVC_CREDENTIALS": true, "PASSWORD": true, "TOKEN": true, "secret": true,
		"ENABLE_X": false, "KEYBOARD": false, "MONKEY_KEY": true, "TOKENIZER": false, "KEY": false,
		"PUBLIC_KEYS": false, "ENABLE_GUEST_ACCESS": false,
	} {
		if got := SecretLikeName(name); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}
