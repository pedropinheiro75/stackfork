package dashboard

import (
	"os"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// A warning that repeats the `down` command must not add a second copy of it
// (app.js commandsOf does the same).
func TestCLICommandsSkipsWarningRepeatingDown(t *testing.T) {
	down := "stackfork down --instance sf-x --volumes"
	got := cliCommands(inventory.Instance{Project: "sf-x", Warnings: []string{
		"orphaned; run `" + down + "`", "orphaned; run `stackfork gc --apply`", "again `stackfork gc --apply`",
	}})
	want := []string{down, "stackfork gc --apply"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("cliCommands = %q, want %q", got, want)
	}
}

// .menu-item sets display:flex at the same specificity as .mobile-only's
// display:none, so desktop menus listed the mobile duplicates. The compound
// selector must exist and come before the <720px block that re-shows it.
func TestMenuItemMobileOnlyHiddenOnDesktop(t *testing.T) {
	b, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	hide := strings.Index(css, ".menu-item.mobile-only { display: none; }")
	show := strings.Index(css, ".mobile-only, .menu-item.mobile-only { display: flex; }")
	if hide < 0 || show < 0 || hide > show {
		t.Fatalf("hide rule at %d, mobile re-show rule at %d", hide, show)
	}
}

func TestAppJSOnlyLinksWebURLsAndShowInstanceUsesChevronPath(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{"function isWebUrl", "isWebUrl(app.url) && app.running", "isWebUrl(ep.url)", `chev.setAttribute("aria-expanded", "true")`, "setCollapsed(chev.dataset.project, false)"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	if strings.Contains(js, "decorateRunnables") {
		t.Error("dead decorateRunnables is back")
	}
}
