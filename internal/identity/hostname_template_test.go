package identity

import (
	"strings"
	"testing"
)

const testHash = "977bbb347841"

func TestRenderHostname(t *testing.T) {
	long := strings.Repeat("x", 30)
	x24 := strings.Repeat("x", 24)
	for _, tc := range []struct {
		name, tmpl, app, worktree, want string
	}{
		{"default main", "", "acme-web", "acme-web", "acme-web.localhost"},
		{"default worktree", "{app}-{worktree}", "acme-web", "feature-invoices", "acme-web-feature-invoices.localhost"},
		{"hash main", "{app}-{worktree}-{hash}", "acme-web", "acme-web", "acme-web-" + testHash + ".localhost"},
		{"hash worktree", "{app}-{worktree}-{hash}", "acme-web", "feature-invoices", "acme-web-feature-invoices-" + testHash + ".localhost"},
		{"prefix main", "stackfork-{app}-{worktree}", "acme-web", "acme-web", "stackfork-acme-web.localhost"},
		{"prefix worktree", "stackfork-{app}-{worktree}", "acme-web", "feature-invoices", "stackfork-acme-web-feature-invoices.localhost"},
		{"worktree main", "{worktree}", "acme-web", "acme-web", "acme-web.localhost"},
		{"worktree worktree", "{worktree}", "acme-web", "feature-invoices", "feature-invoices.localhost"},
		{"old main", "{worktree}-{hash}", "acme-web", "acme-web", "acme-web-" + testHash + ".localhost"},
		{"old worktree", "{worktree}-{hash}", "acme-web", "feature-invoices", "feature-invoices-" + testHash + ".localhost"},
		{"slug", "{worktree}", "acme-web", "Feature/Invoices V2", "feature-invoices-v2.localhost"},
		{"long worktree kept whole", "{app}-{worktree}", "cabo-nuxt", "128-account-management-v2", "cabo-nuxt-128-account-management-v2.localhost"},
		{"no truncation when it fits", "{worktree}", "acme-web", long, long + ".localhost"},
		{"truncate 24 over 63", "{app}-{worktree}", strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("a", 24) + "-" + strings.Repeat("b", 24) + ".localhost"},
		{"empty folder", "{worktree}", "acme-web", "---", "app.localhost"},
		{"longest preset fits", "{app}-{worktree}-{hash}", long, strings.Repeat("y", 30), x24 + "-" + strings.Repeat("y", 24) + "-" + testHash + ".localhost"},
		{"over-long falls back", "{app}-{worktree}-{hash}-{hash}-{hash}", long, strings.Repeat("y", 30), strings.Repeat("y", 24) + "-" + testHash + ".localhost"},
		{"empty falls back", "-", "acme-web", "feature-invoices", "feature-invoices-" + testHash + ".localhost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderHostname(tc.tmpl, tc.app, tc.worktree, testHash)
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
			if !hostnamePattern.MatchString(got) {
				t.Fatalf("%s is not a valid hostname", got)
			}
		})
	}
}

func TestValidateHostnameTemplate(t *testing.T) {
	for _, ok := range []string{"", "{app}-{worktree}", "{app}-{worktree}-{hash}", "stackfork-{app}-{worktree}", "{worktree}-{hash}", "dev-{worktree}"} {
		if err := ValidateHostnameTemplate(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"{branch}", "{app}.{worktree}", "{App}", "a b", "{app", "x_{worktree}"} {
		err := ValidateHostnameTemplate(bad)
		if err == nil || !strings.Contains(err.Error(), "{worktree}") {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func TestUniqueHostname(t *testing.T) {
	none := func(string) bool { return false }
	held := func(h string) bool { return h == "acme-web.localhost" }
	all := func(string) bool { return true }
	for _, tc := range []struct {
		name, in string
		taken    func(string) bool
		want     string
		changed  bool
	}{
		{"free", "acme-web.localhost", none, "acme-web.localhost", false},
		{"reserved", "stackfork.localhost", none, "stackfork-977bbb.localhost", true},
		{"held", "acme-web.localhost", held, "acme-web-977bbb.localhost", true},
		{"over-long suffix falls back", strings.Repeat("x", 62) + ".localhost", all, strings.Repeat("x", 24) + "-" + testHash + ".localhost", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := UniqueHostname(tc.in, testHash, tc.taken)
			if got != tc.want || changed != tc.changed {
				t.Fatalf("got %s %v, want %s %v", got, changed, tc.want, tc.changed)
			}
		})
	}
}

// Instances with no saved hostname keep resolving to the old derived name.
func TestBrowserHostnameWithoutSavedKeepsOldName(t *testing.T) {
	got, err := BrowserHostname("sf-feature-977bbb347841", "")
	if err != nil || got != "feature-977bbb347841.localhost" {
		t.Fatalf("got %s, %v", got, err)
	}
}
