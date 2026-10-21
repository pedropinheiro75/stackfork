package knowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeEntryK(t *testing.T, extra string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "knowledge.json")
	body := `{"schema":2,"status":"draft","facts":{},"plan":{}` + extra + `}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEntryLinksRoundTrip(t *testing.T) {
	p := writeEntryK(t, `,"entry":{"service":"nginx","port":80,"path":"/app"},"links":[{"name":"Dashboard","path":"/admin"},{"name":"Email","service":"smtp4dev","port":80},{"name":"Alt","port":8080}]`)
	k, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if k.Entry == nil || *k.Entry != (Entry{Service: "nginx", Port: 80, Path: "/app"}) || len(k.Links) != 3 {
		t.Fatalf("%+v %+v", k.Entry, k.Links)
	}
	out := filepath.Join(t.TempDir(), "k.json")
	if err := k.Save(out); err != nil {
		t.Fatal(err)
	}
	k2, err := Load(out)
	if err != nil || !reflect.DeepEqual(k.Entry, k2.Entry) || !reflect.DeepEqual(k.Links, k2.Links) {
		t.Fatalf("%+v %v", k2, err)
	}
	none, err := Load(writeEntryK(t, `,"entry":{"none":true}`))
	if err != nil || none.Entry == nil || !none.Entry.None {
		t.Fatalf("%+v %v", none, err)
	}
}

// Task 1.1: every rule of D2 refuses the file, naming the field and index,
// and no error text contains the refused value.
func TestEntryLinksLoadRefusals(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 17; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"name":"L` + strings.Repeat("a", i) + `","path":"/x"}`)
	}
	many := sb.String()
	for name, tc := range map[string]struct{ doc, want, secret string }{
		"protocol-relative":  {`"links":[{"name":"A","path":"//evil.example/"}]`, "links[0]", "evil.example"},
		"absolute url":       {`"links":[{"name":"A","path":"https://example.com"}]`, "links[0]", "example.com"},
		"https as service":   {`"entry":{"service":"https://x","port":80}`, "entry", "https://x"},
		"dotdot":             {`"links":[{"name":"A","path":"/a/../b"}]`, "links[0]", "/a/../b"},
		"encoded dotdot":     {`"links":[{"name":"A","path":"/a/%2e%2e/b"}]`, "links[0]", "%2e%2e"},
		"tab":                {`"links":[{"name":"A","path":"/a\tb"}]`, "links[0]", "/a"},
		"space":              {`"links":[{"name":"A","path":"/a b"}]`, "links[0]", "/a b"},
		"backslash":          {`"links":[{"name":"A","path":"/a\\b"}]`, "links[0]", `a\b`},
		"non ascii":          {`"links":[{"name":"A","path":"/café"}]`, "links[0]", "caf"},
		"too long path":      {`"links":[{"name":"A","path":"/` + strings.Repeat("a", 256) + `"}]`, "links[0]", strings.Repeat("a", 20)},
		"17 links":           {`"links":[` + many + `]`, "links", ""},
		"duplicate case":     {`"links":[{"name":"Docs","path":"/a"},{"name":"dOcS","path":"/b"}]`, "links[1]", "dOcS"},
		"App name":           {`"links":[{"name":"App","path":"/a"}]`, "links[0]", ""},
		"app name case":      {`"links":[{"name":"aPp","path":"/a"}]`, "links[0]", "aPp"},
		"empty name":         {`"links":[{"name":"","path":"/a"}]`, "links[0]", ""},
		"long name":          {`"links":[{"name":"` + strings.Repeat("n", 33) + `","path":"/a"}]`, "links[0]", strings.Repeat("n", 33)},
		"bad name char":      {`"links":[{"name":"A<b>","path":"/a"}]`, "links[0]", "A<b>"},
		"padded name":        {`"links":[{"name":" A","path":"/a"}]`, "links[0]", ""},
		"none with service":  {`"entry":{"none":true,"service":"web"}`, "entry", ""},
		"none with proposed": {`"entry":{"none":true,"proposed":true}`, "entry", ""},
		"unknown entry key":  {`"entry":{"service":"web","port":80,"url":"http://x"}`, `"url"`, "http://x"},
		"unknown link key":   {`"links":[{"name":"A","path":"/a","href":"http://x"}]`, `"href"`, "http://x"},
		"port 0":             {`"entry":{"service":"web","port":0}`, "entry", ""},
		"port too high":         {`"entry":{"service":"web","port":70001}`, "entry", "70001"},
		"port string":        {`"entry":{"service":"web","port":"80"}`, "entry", ""},
		"entry no service":   {`"entry":{"port":80}`, "entry", ""},
		"entry no port":      {`"entry":{"service":"web"}`, "entry", ""},
		"link service only":  {`"links":[{"name":"A","service":"web"}]`, "links[0]", ""},
		"link empty":         {`"links":[{"name":"A"}]`, "links[0]", ""},
		"links not a list":   {`"links":{"a":"/x"}`, "links", ""},
		"bad service":        {`"links":[{"name":"A","service":"-x","port":80}]`, "links[0]", ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeEntryK(t, ","+tc.doc))
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
			if tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("error echoes the refused value: %q", err)
			}
		})
	}
}

func TestSaveValidatesEntryLinks(t *testing.T) {
	k := &Knowledge{Links: []Link{{Name: "A", Path: "//evil.example"}}}
	err := k.Save(filepath.Join(t.TempDir(), "k.json"))
	if err == nil || strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("%v", err)
	}
}

func TestUpsertRemoveLink(t *testing.T) {
	k := &Knowledge{}
	k.UpsertLink(Link{Name: "email", Service: "a", Port: 1})
	k.UpsertLink(Link{Name: "Docs", Path: "/d"})
	k.UpsertLink(Link{Name: "Email", Service: "mailpit", Port: 8025})
	if len(k.Links) != 2 || k.Links[0].Name != "Email" || k.Links[0].Service != "mailpit" {
		t.Fatalf("%+v", k.Links)
	}
	if !k.RemoveLink("DOCS") || k.RemoveLink("docs") || len(k.Links) != 1 {
		t.Fatalf("%+v", k.Links)
	}
}

// Task 1.2: entry and links are outside the trust hash.
func TestTrustIgnoresEntryAndLinks(t *testing.T) {
	k, err := Load(filepath.Join("testdata", "knowledge2.json"))
	if err != nil {
		t.Fatal(err)
	}
	before := PlanHash(k.Plan.Steps)
	k.Entry = &Entry{Service: "web", Port: 3000, Path: "/app"}
	k.Links = []Link{{Name: "Dashboard", Path: "/admin"}}
	if PlanHash(k.Plan.Steps) != before {
		t.Fatal("entry and links changed the plan hash")
	}
	out := filepath.Join(t.TempDir(), "k.json")
	if err := k.Save(out); err != nil {
		t.Fatal(err)
	}
	r, err := Load(out)
	if err != nil || PlanHash(r.Plan.Steps) != before {
		t.Fatalf("%v", err)
	}
}

// Task 1.3.
func TestMarkdownEntryAndLinks(t *testing.T) {
	k := &Knowledge{
		Entry: &Entry{Service: "nginx", Port: 80, Path: "/app"},
		Links: []Link{{Name: "Dashboard", Path: "/admin"}, {Name: "Email", Service: "smtp4dev", Port: 80}},
	}
	want := "## Entry and links\n\n- App: nginx:80/app\n- Dashboard: /admin (on the entry)\n- Email: smtp4dev:80\n\n"
	if md := k.Markdown(); !strings.Contains(md, want) {
		t.Fatalf("%s", md)
	}
	k.Entry = &Entry{None: true}
	k.Links = nil
	if md := k.Markdown(); !strings.Contains(md, "## Entry and links\n\n- App: none (no web entry)\n\n") {
		t.Fatalf("%s", md)
	}
	k.Entry = &Entry{Service: "web", Port: 3000, Proposed: true}
	if md := k.Markdown(); !strings.Contains(md, "- App: web:3000 (proposed by the scan, not confirmed)\n") {
		t.Fatalf("%s", md)
	}
	if md := (&Knowledge{}).Markdown(); strings.Contains(md, "Entry and links") {
		t.Fatal("empty section rendered")
	}
}
