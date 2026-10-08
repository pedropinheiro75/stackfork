package dashboard_test

import (
	"os"
	"strings"
	"testing"
)

// A failed start / worktree-add must be shown on the repo row with the exact
// CLI command to retry it, not only in the activity log (add-repo-browser
// D6a). The script is static, so this pins the contract it must keep.
func TestAppJSShowsRepoErrorsWithRetryCommand(t *testing.T) {
	js, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	css, _ := os.ReadFile("static/app.css")
	for _, want := range []string{
		`reposState.errors[d.repo] = `, `renderRepoErrors();`, `setAttribute("role", "alert")`,
		`"stackfork start-env " + shq(d.path)`, `"stackfork worktree add " + shq(d.repo)`, `data-dismiss-repo`, `dismissRepo`,
		`if (!o.ok) fail(o.message)`, `b.ok === false`,
	} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	// the message is inserted as text, never as HTML
	if strings.Contains(string(js), "box.innerHTML") {
		t.Error("repo error must be built with textContent")
	}
	if !strings.Contains(string(css), ".repo-error {") {
		t.Error("app.css has no .repo-error style")
	}
}
