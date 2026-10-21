package knowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeLive(t *testing.T, liveJSON string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "knowledge.json")
	body := `{"schema":2,"status":"draft","facts":{},"plan":{}`
	if liveJSON != "" {
		body += `,"live":` + liveJSON
	}
	if err := os.WriteFile(p, []byte(body+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLiveRoundTrip(t *testing.T) {
	p := writeLive(t, `{"watch":[{"path":"infra/web.Dockerfile","action":"rebuild","service":"web"},{"path":"db/schema","action":"migrate"}],"ignore":["package.json"]}`)
	k, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Live{Watch: []LiveWatch{{"infra/web.Dockerfile", "rebuild", "web"}, {"db/schema", "migrate", ""}}, Ignore: []string{"package.json"}}
	if !reflect.DeepEqual(k.Live, want) {
		t.Fatalf("%+v", k.Live)
	}
	if err := k.Save(p); err != nil {
		t.Fatal(err)
	}
	k2, err := Load(p)
	if err != nil || !reflect.DeepEqual(k2.Live, want) {
		t.Fatalf("%+v %v", k2, err)
	}
}

func TestLiveAbsentOmitted(t *testing.T) {
	p := writeLive(t, "")
	k, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = k.Save(p)
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), `"live"`) {
		t.Fatalf("%s", b)
	}
}

func TestLiveLoadRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":      `{"watchh":[]}`,
		"unknown entry":    `{"watch":[{"path":"a","action":"prepare","cmd":"x"}]}`,
		"unknown action":   `{"watch":[{"path":"a","action":"nuke"}]}`,
		"rebuild no svc":   `{"watch":[{"path":"a","action":"rebuild"}]}`,
		"svc on migrate":   `{"watch":[{"path":"a","action":"migrate","service":"web"}]}`,
		"dotdot":           `{"watch":[{"path":"../x","action":"prepare"}]}`,
		"absolute":         `{"watch":[{"path":"/x","action":"prepare"}]}`,
		"backslash":        `{"watch":[{"path":"a\\b","action":"prepare"}]}`,
		"dot prefix":       `{"watch":[{"path":"./a","action":"prepare"}]}`,
		"ignore dotdot":    `{"ignore":["a/../../b"]}`,
		"not an object":    `["a"]`,
		"watch not a list": `{"watch":"a"}`,
	} {
		if _, err := Load(writeLive(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLiveDoesNotChangeTrustHash(t *testing.T) {
	k := &Knowledge{Plan: Plan{Steps: []Step{{ID: "app", Kind: KindHost, Cmd: "x"}}}}
	h := PlanHash(k.Plan.Steps)
	if err := k.UpsertLiveWatch(LiveWatch{Path: "db", Action: LiveMigrate}); err != nil {
		t.Fatal(err)
	}
	_ = k.AddLiveIgnore("package.json")
	if PlanHash(k.Plan.Steps) != h {
		t.Fatal("live changed the trust hash")
	}
}

func TestLiveEdits(t *testing.T) {
	k := &Knowledge{}
	_ = k.UpsertLiveWatch(LiveWatch{Path: "db", Action: LiveMigrate})
	_ = k.UpsertLiveWatch(LiveWatch{Path: "db", Action: LivePrepare})
	if len(k.Live.Watch) != 1 || k.Live.Watch[0].Action != LivePrepare {
		t.Fatalf("%+v", k.Live)
	}
	if k.UpsertLiveWatch(LiveWatch{Path: "x", Action: LiveRebuild}) == nil || len(k.Live.Watch) != 1 {
		t.Fatal("invalid entry kept")
	}
	if !k.RemoveLiveWatch("db") || k.RemoveLiveWatch("db") {
		t.Fatal("remove")
	}
	_ = k.AddLiveIgnore("a")
	_ = k.AddLiveIgnore("a")
	if len(k.Live.Ignore) != 1 || !k.RemoveLiveIgnore("a") || !k.Live.IsZero() {
		t.Fatalf("%+v", k.Live)
	}
}
