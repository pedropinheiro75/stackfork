package dashboard_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

// While the first inventory is loading, Docker and Proxy read "checking" in
// the busy state - never failed or off.
func TestLoadingStatusIsBusyNotFailed(t *testing.T) {
	srv, _, _, _ := seedStack(t)
	old := dashboard.IndexWait
	dashboard.IndexWait = 50 * time.Millisecond
	defer func() { dashboard.IndexWait = old }()
	t.Setenv("DOCKER_FAKE_PS_SLEEP", "1")
	body := indexBody(srv)
	for _, id := range []string{"docker-status", "proxy-status"} {
		re := regexp.MustCompile(`(?s)<span id="` + id + `".*?</span></span>`)
		el := re.FindString(body)
		if el == "" {
			t.Fatalf("#%s missing", id)
		}
		if !strings.Contains(el, `data-state="busy"`) || !strings.Contains(el, "checking&hellip;") {
			t.Errorf("#%s must be busy and read checking while loading:\n%s", id, el)
		}
	}
}

func TestRailHasFourViews(t *testing.T) {
	srv := newTestServer(t)
	body := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	rail := regexp.MustCompile(`(?s)<nav class="rail".*?</nav>`).FindString(body)
	if rail == "" {
		t.Fatal("nav.rail missing")
	}
	items := regexp.MustCompile(`(?s)<button[^>]*data-view="([a-z]+)"[^>]*>(.*?)</button>`).FindAllStringSubmatch(rail, -1)
	if len(items) != 4 {
		t.Fatalf("want 4 rail items, got %d:\n%s", len(items), rail)
	}
	for i, want := range []struct{ view, name string }{{"instances", "Instances"}, {"repos", "Repos"}, {"cleanup", "Clean up"}, {"settings", "Settings"}} {
		if items[i][1] != want.view || !strings.Contains(items[i][2], want.name) {
			t.Errorf("item %d: got %q %q, want %q %q", i, items[i][1], items[i][2], want.view, want.name)
		}
		if !strings.Contains(body, `id="view-`+want.view+`"`) {
			t.Errorf("missing #view-%s", want.view)
		}
	}
	if !regexp.MustCompile(`data-view="instances"[^>]*aria-current="page"`).MatchString(rail) {
		t.Errorf("Instances must carry aria-current=page:\n%s", rail)
	}
	if strings.Count(rail, `aria-current`) != 1 {
		t.Errorf("exactly one item is current:\n%s", rail)
	}
}
