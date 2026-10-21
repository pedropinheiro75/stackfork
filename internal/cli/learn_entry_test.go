package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func learnRec(t *testing.T, root string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runCLI(t, append([]string{"--root", root, "learn", "record"}, args...)...)
}

// entry-links task 3.2: each form, in any flag order.
func TestLearnRecordEntryAndLinks(t *testing.T) {
	root, path := seedKnowledge(t)
	load := func() *knowledge.Knowledge {
		k, err := knowledge.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	// A path link needs an entry first.
	if _, stderr, code := learnRec(t, root, "link", "Dashboard", "--path", "/admin"); code == 0 || !strings.Contains(stderr, "learn record entry") {
		t.Fatalf("path link without entry: %d %s", code, stderr)
	}
	out, stderr, code := learnRec(t, root, "entry", "--port", "80", "--service", "nginx", "--path", "/app")
	if code != 0 || !strings.Contains(out, `"recorded": "entry"`) {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	if k := load(); k.Entry == nil || *k.Entry != (knowledge.Entry{Service: "nginx", Port: 80, Path: "/app"}) || k.Status != knowledge.StatusDraft {
		t.Fatalf("%+v", k.Entry)
	}
	for _, a := range [][]string{
		{"link", "Dashboard", "--path", "/admin"},
		{"link", "--service", "smtp4dev", "--port", "80", "Email"},
		{"link", "email", "--service", "mailpit", "--port", "8025", "--path", "/inbox"},
	} {
		if _, stderr, code := learnRec(t, root, a...); code != 0 {
			t.Fatalf("%v: %s", a, stderr)
		}
	}
	want := []knowledge.Link{{Name: "Dashboard", Path: "/admin"}, {Name: "email", Service: "mailpit", Port: 8025, Path: "/inbox"}}
	if k := load(); !reflect.DeepEqual(k.Links, want) {
		t.Fatalf("%+v", k.Links)
	}
	if _, stderr, code := learnRec(t, root, "link", "--remove", "EMAIL"); code != 0 {
		t.Fatal(stderr)
	}
	if k := load(); len(k.Links) != 1 {
		t.Fatalf("%+v", k.Links)
	}
	if _, stderr, code := learnRec(t, root, "link", "--remove", "Email"); code == 0 || !strings.Contains(stderr, "no link") {
		t.Fatalf("%d %s", code, stderr)
	}
	// none, and a path link is then refused.
	if _, stderr, code := learnRec(t, root, "entry", "--none"); code != 0 {
		t.Fatal(stderr)
	}
	if k := load(); k.Entry == nil || !k.Entry.None {
		t.Fatalf("%+v", k.Entry)
	}
	if _, _, code := learnRec(t, root, "link", "Docs", "--path", "/docs"); code == 0 {
		t.Fatal("path link on none")
	}
	if _, stderr, code := learnRec(t, root, "entry", "--remove"); code != 0 {
		t.Fatal(stderr)
	}
	if k := load(); k.Entry != nil {
		t.Fatalf("%+v", k.Entry)
	}
	if _, _, code := learnRec(t, root, "entry", "--remove"); code == 0 {
		t.Fatal("removing an absent entry")
	}
}

func TestLearnRecordEntryRefusals(t *testing.T) {
	root, path := seedKnowledge(t)
	learnRec(t, root, "entry", "--service", "web", "--port", "3000")
	before, _ := os.ReadFile(path)
	for _, a := range [][]string{
		{"entry"},
		{"entry", "--service", "web"},
		{"entry", "--port", "80"},
		{"entry", "--none", "--service", "web", "--port", "80"},
		{"entry", "--remove", "--port", "80"},
		{"entry", "--service", "web", "--port", "80", "extra"},
		{"entry", "--service", "web", "--port", "99999"},
		{"entry", "--service", "web", "--port", "80", "--path", "//evil.example"},
		{"entry", "--service", "web", "--port", "80", "--path", "https://evil.example"},
		{"link"},
		{"link", "A", "B", "--path", "/x"},
		{"link", "A"},
		{"link", "A", "--service", "web"},
		{"link", "A", "--path", "/x", "--service", "web", "--port", "80", "--remove", "A"},
		{"link", "App", "--path", "/x"},
		{"link", "A", "--path", "//evil.example"},
		{"link", "A", "--path", "/..", "--service", "web", "--port", "3000"},
	} {
		_, stderr, code := learnRec(t, root, a...)
		if code == 0 {
			t.Errorf("%v accepted", a)
		}
		if strings.Contains(stderr, "evil.example") {
			t.Errorf("%v: error echoes the value: %s", a, stderr)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatalf("%v changed the file", a)
		}
	}
}

// Without a prepared instance the scanned services are the check, with a note.
func TestLearnRecordEntryChecksScannedServices(t *testing.T) {
	root, path := seedKnowledge(t)
	k, _ := knowledge.Load(path)
	k.Facts.Services = []knowledge.Service{{Name: "web", Ports: []string{"3000:3000"}}, {Name: "db", Ports: []string{"5432:5432"}}}
	k.Save(path)
	_, stderr, code := learnRec(t, root, "entry", "--service", "nginx", "--port", "80")
	if code == 0 || !strings.Contains(stderr, "web (3000)") || !strings.Contains(stderr, "db (5432)") {
		t.Fatalf("%d %s", code, stderr)
	}
	if _, stderr, code := learnRec(t, root, "entry", "--service", "web", "--port", "8080"); code == 0 || !strings.Contains(stderr, "web (3000)") {
		t.Fatalf("%d %s", code, stderr)
	}
	out, stderr, code := learnRec(t, root, "entry", "--service", "web", "--port", "3000")
	if code != 0 || !strings.Contains(out, "adapter may add") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
}

// With a prepared instance its Compose model is the check.
func TestLearnRecordEntryChecksPreparedInstance(t *testing.T) {
	_, wt, _, flags, _ := worktreeFixture(t)
	k := &knowledge.Knowledge{Status: knowledge.StatusDraft}
	if err := k.Save(filepath.Join(wt, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	rec := func(args ...string) (string, string, int) {
		return runCLI(t, append(append(append([]string{}, flags...), "--root", wt, "learn", "record"), args...)...)
	}
	_, stderr, code := rec("entry", "--service", "nginx", "--port", "80")
	if code == 0 || !strings.Contains(stderr, "web (8080)") || !strings.Contains(stderr, "db") {
		t.Fatalf("%d %s", code, stderr)
	}
	if _, stderr, code := rec("link", "Email", "--service", "web", "--port", "9"); code == 0 || !strings.Contains(stderr, "web (8080)") {
		t.Fatalf("%d %s", code, stderr)
	}
	out, stderr, code := rec("entry", "--service", "web", "--port", "8080")
	if code != 0 || strings.Contains(out, "adapter may add") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
}

func TestLearnRecordEntryHelp(t *testing.T) {
	root, _ := seedKnowledge(t)
	for _, kind := range []string{"entry", "link"} {
		out, _, code := learnRec(t, root, kind, "--help")
		if code != 0 || !strings.Contains(out, "learn record "+kind) {
			t.Fatalf("%s: %d %s", kind, code, out)
		}
	}
	out, _, _ := runCLI(t, "--root", root, "learn", "--help")
	if !strings.Contains(out, "entry|link") {
		t.Fatalf("%s", out)
	}
}

// Dropping the entry leaves path-only links without a target; say which.
func TestLearnRecordEntryNoneNamesOrphanLinks(t *testing.T) {
	root, _ := seedKnowledge(t)
	for _, a := range [][]string{
		{"entry", "--port", "80", "--service", "nginx"},
		{"link", "Dashboard", "--path", "/admin"},
		{"link", "Email", "--service", "smtp4dev", "--port", "80"},
	} {
		if _, stderr, code := learnRec(t, root, a...); code != 0 {
			t.Fatalf("%v: %s", a, stderr)
		}
	}
	for _, flag := range []string{"--none", "--remove"} {
		out, stderr, code := learnRec(t, root, "entry", flag)
		all := out + stderr
		if code != 0 || !strings.Contains(all, "these links follow the entry and now have no target: Dashboard\"") {
			t.Fatalf("%s: %d %s", flag, code, all)
		}
		if _, _, code := learnRec(t, root, "entry", "--port", "80", "--service", "nginx"); code != 0 {
			t.Fatal("re-record")
		}
	}
}
