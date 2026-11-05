package dashboard

import (
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
)

func TestAddressOptions(t *testing.T) {
	selected := func(cfg *config.Config) (string, int) {
		opts := addressOptions(cfg)
		for _, o := range opts {
			if o.Selected {
				return o.Value, len(opts)
			}
		}
		return "", len(opts)
	}
	if v, n := selected(nil); v != "{app}-{worktree}" || n != 5 {
		t.Fatalf("default: %q %d", v, n)
	}
	if v, n := selected(&config.Config{Hostname: "{worktree}"}); v != "{worktree}" || n != 5 {
		t.Fatalf("preset: %q %d", v, n)
	}
	if v, n := selected(&config.Config{Hostname: "dev-{worktree}"}); v != "dev-{worktree}" || n != 6 {
		t.Fatalf("custom: %q %d", v, n)
	}
	if got := addressOptions(nil)[0].Text; got != "App and worktree (default): acme-web-feature-invoices.localhost" {
		t.Fatalf("text: %q", got)
	}
}
