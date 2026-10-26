package dashboard_test

import (
	"strings"
	"testing"
)

// The Project page shell: the view, its styles and its scripts are in the index,
// and the script order puts the page after the two parts it wires.
func TestProjectViewIsInIndexWithScripts(t *testing.T) {
	srv := newTestServer(t)
	body := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	for _, want := range []string{`<main id="view-project" class="view" hidden>`, `id="project-body"`, `href="/static/project.css"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index is missing %s", want)
		}
	}
	last := -1
	for _, js := range []string{"/static/app.js", "/static/project-workflow.js", "/static/project-resources.js", "/static/project.js"} {
		i := strings.Index(body, `<script src="`+js+`">`)
		if i < 0 || i < last {
			t.Errorf("%s is missing or out of order", js)
		}
		last = i
	}
	if strings.Contains(body, `id="view-project" class="view" hidden aria-live`) {
		t.Error("the project view must not be one big live region: polling would be read out")
	}
	js := doRequest(srv, "GET", "/static/project.js", "127.0.0.1:55123", nil)
	if js.Code != 200 || !strings.Contains(js.Body.String(), "sfProject") {
		t.Errorf("project.js: %d", js.Code)
	}
}

// The Repos list links each repository name to its page and the expanded row has Open project.
func TestProjectRepoNameLinksToThePage(t *testing.T) {
	e := newBrowserEnv(t)
	e.readyRepo("ready")
	body := e.get("/fragments/repos").Body.String()
	for _, want := range []string{`<a class="repo-name" href="#project/ready"`, `class="btn btn-ghost repo-open-project" href="#project/ready"`, `aria-label="Worktrees of ready"`} {
		if !strings.Contains(body, want) {
			t.Errorf("repos fragment is missing %s", want)
		}
	}
}

// project.js renders the fixture (shape of docs/design/project-page/api.md) in a Node stub.
func TestProjectPageScriptRendersFixtureAndRoutes(t *testing.T) {
	runAppJS(t, "project.case.js")
}
