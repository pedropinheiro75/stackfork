package dashboard

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

func inst(app, project, branch, state string, main bool, used time.Time) inventory.Instance {
	return inventory.Instance{
		Project: project, App: app, Branch: branch, State: state, IsMain: main, Used: used,
		Root: "/src/" + app, RelRoot: "main checkout",
	}
}

// TestBuildProjectsGroupsAndSorts covers the sort in DESIGN.md "Instances:
// grouped by project": live projects first, then newest activity, then name;
// inside a project live versions, the main checkout, then branch.
func TestBuildProjectsGroupsAndSorts(t *testing.T) {
	now := time.Now()
	ps := buildProjects([]inventory.Instance{
		inst("notes-cli", "sf-n1", "main", "stopped", true, now.Add(-time.Hour)),
		inst("acme-web", "sf-a1", "fix-invoices", "stopped", false, now),
		inst("acme-web", "sf-a2", "main", "stopped", true, now.Add(-2*time.Hour)),
		inst("acme-web", "sf-a3", "feature-login", "partial", false, now.Add(-3*time.Hour)),
		inst("billing-api", "sf-b1", "main", "running", true, now.Add(-24*time.Hour)),
		inst("aaa", "sf-z1", "main", "stopped", true, now.Add(-time.Hour)),
	})
	var apps []string
	for _, p := range ps {
		apps = append(apps, p.App)
	}
	// live (acme-web has the newer manifest), then stopped by activity, ties by name
	if got := strings.Join(apps, ","); got != "acme-web,billing-api,aaa,notes-cli" {
		t.Fatalf("project order: %s", got)
	}
	var branches []string
	for _, v := range ps[0].Versions {
		branches = append(branches, v.Branch)
	}
	if got := strings.Join(branches, ","); got != "feature-login,main,fix-invoices" {
		t.Fatalf("version order: %s", got)
	}
	if len(ps[0].Chips) != 1 || ps[0].Chips[0].Label != "1 partial" {
		t.Fatalf("summary chips: %+v", ps[0].Chips)
	}
	if c := ps[2].Chips; len(c) != 1 || c[0].Label != "Stopped" {
		t.Fatalf("all-stopped project chip: %+v", c)
	}
}

// TestProjectCardRendersAppOnceAndOneHostChip covers: two versions of one app
// share a card with the app name once, and five dev scripts' single knowledge
// link prints no hint inline (title and menu item only).
func TestProjectCardRendersAppOnceAndOneHostChip(t *testing.T) {
	hint := "host app: not running — start with `npm run dev`, then `stackfork route add <name> <port>`"
	a := inst("acme-web", "sf-acme-web-2ffa6841c75d", "feature-login", "stopped", false, time.Now())
	a.Apps = []inventory.AppLink{{Label: "App", Source: "knowledge", Hint: hint}}
	b := inst("acme-web", "sf-acme-web-9b21e07d4c3a", "main", "stopped", true, time.Now())
	ps := buildProjects([]inventory.Instance{a, b})
	if len(ps) != 1 {
		t.Fatalf("want one project, got %d", len(ps))
	}
	var buf bytes.Buffer
	if err := indexTemplate.ExecuteTemplate(&buf, "project", ps[0]); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "\n") {
		t.Error("card markup must have no line breaks (it has to equal app.js's output)")
	}
	if n := strings.Count(html, "<h2"); n != 1 {
		t.Errorf("want one project heading, got %d", n)
	}
	if n := strings.Count(html, ">acme-web<"); n != 1 {
		t.Errorf("app name must print once, got %d", n)
	}
	if n := strings.Count(html, "on host</span>"); n != 1 {
		t.Errorf("want exactly one on-host chip, got %d:\n%s", n, html)
	}
	if strings.Count(html, "How to run the host app") != 1 {
		t.Error("the version menu must carry the host app item once")
	}
	chip := html[strings.Index(html, `class="app-chip stopped host"`):]
	if chip = chip[:strings.Index(chip, "</span>")]; strings.Contains(chip, "npm run dev") && !strings.Contains(chip, `title="`) {
		t.Errorf("the hint belongs in the title only: %s", chip)
	}
	if strings.Contains(html[strings.Index(html, ">App"):], "App &middot; host app") {
		t.Error("the hint must not print inline")
	}
	if !strings.Contains(html, "2 versions") {
		t.Errorf("version count missing: %s", html)
	}
}
