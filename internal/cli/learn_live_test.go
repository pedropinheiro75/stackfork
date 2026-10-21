package cli

import (
	"reflect"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestLearnRecordLiveWatchAndIgnore(t *testing.T) {
	root, path := seedKnowledge(t)
	rec := func(args ...string) (string, int) {
		_, stderr, code := runCLI(t, append([]string{"--root", root, "learn", "record"}, args...)...)
		return stderr, code
	}
	for _, a := range [][]string{
		{"live-watch", "infra/web.Dockerfile", "--action", "rebuild", "--service", "web"},
		{"live-watch", "--action", "migrate", "db/schema"},
		{"live-ignore", "package.json"},
	} {
		if e, c := rec(a...); c != 0 {
			t.Fatalf("%v: %s", a, e)
		}
	}
	k, _ := knowledge.Load(path)
	want := knowledge.Live{
		Watch:  []knowledge.LiveWatch{{Path: "infra/web.Dockerfile", Action: "rebuild", Service: "web"}, {Path: "db/schema", Action: "migrate"}},
		Ignore: []string{"package.json"},
	}
	if !reflect.DeepEqual(k.Live, want) {
		t.Fatalf("%+v", k.Live)
	}

	for _, a := range [][]string{
		{"live-watch", "x", "--action", "rebuild"},
		{"live-watch", "x", "--action", "nuke"},
		{"live-watch", "../x", "--action", "prepare"},
		{"live-watch", "/x", "--action", "prepare"},
		{"live-ignore", "../x"},
		{"live-ignore", "a", "--action", "rebuild"},
		{"live-watch", "--remove", "never/recorded"},
	} {
		if _, c := rec(a...); c == 0 {
			t.Errorf("%v accepted", a)
		}
	}
	if e, c := rec("live-watch", "--remove", "db/schema"); c != 0 {
		t.Fatal(e)
	}
	if e, c := rec("live-ignore", "--remove", "package.json"); c != 0 {
		t.Fatal(e)
	}
	k, _ = knowledge.Load(path)
	if len(k.Live.Watch) != 1 || len(k.Live.Ignore) != 0 {
		t.Fatalf("%+v", k.Live)
	}
}

func TestLearnRecordFactMigrationDir(t *testing.T) {
	root, path := seedKnowledge(t)
	rec := func(args ...string) int {
		_, _, code := runCLI(t, append([]string{"--root", root, "learn", "record"}, args...)...)
		return code
	}
	if rec("fact", "migration-dir", "db/changes") != 0 || rec("fact", "migration-dir", "db/changes") != 0 {
		t.Fatal("record")
	}
	k, _ := knowledge.Load(path)
	if !reflect.DeepEqual(k.Facts.MigrationDirs, []string{"db/changes"}) {
		t.Fatalf("%v", k.Facts.MigrationDirs)
	}
	if rec("fact", "migration-dir", "../x") == 0 || rec("fact", "other", "x") == 0 {
		t.Fatal("accepted")
	}
	if rec("fact", "migration-dir", "--remove", "db/changes") != 0 {
		t.Fatal("remove")
	}
	k, _ = knowledge.Load(path)
	if len(k.Facts.MigrationDirs) != 0 {
		t.Fatal("kept")
	}
}
