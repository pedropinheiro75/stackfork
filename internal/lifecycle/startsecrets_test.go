package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServicesListSecret(t *testing.T) {
	snap := `{"services":{
		"plain":{"image":"x","environment":{"A":"1"}},
		"filed":{"image":"x","secrets":[{"source":"s"}]},
		"mapped":{"image":"x","environment":{"TOKEN":"${TOKEN}"}}
	}}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.json"), []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		svc  string
		want bool
	}{{"plain", false}, {"filed", true}, {"mapped", true}, {"missing", false}} {
		if got := servicesListSecret(dir, []string{c.svc}); got != c.want {
			t.Errorf("%s: got %v, want %v", c.svc, got, c.want)
		}
	}
	if !servicesListSecret(t.TempDir(), []string{"plain"}) {
		t.Error("an unreadable snapshot must count as a yes")
	}
}
