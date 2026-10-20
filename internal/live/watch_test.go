package live

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func rels(es []Entry) []string {
	var out []string
	for _, e := range es {
		s := e.Category + ":" + e.Rel
		if e.Service != "" {
			s += "@" + e.Service
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func composeJSON(root string) []byte {
	return []byte(fmt.Sprintf(`{"services":{
	  "web":{"build":{"context":%q,"dockerfile":"Dockerfile"}},
	  "db":{"image":"postgres:16"}}}`, root))
}

func TestWatchSetByPackageManager(t *testing.T) {
	cases := []struct {
		name, pm string
		others   []string
		files    []string
		want     []string
	}{
		{"bun", "bun", nil, []string{"bun.lock", "package.json"}, []string{"build:bun.lock@web"}},
		{"pnpm", "pnpm", nil, []string{"pnpm-lock.yaml", "package.json"}, []string{"build:pnpm-lock.yaml@web"}},
		{"npm", "npm", nil, []string{"package-lock.json", "package.json"}, []string{"build:package-lock.json@web"}},
		{"npm without lockfile", "npm", nil, []string{"package.json"}, []string{"build:package.json@web"}},
		{"go", "", []string{"go.mod"}, []string{"go.mod", "go.sum"}, []string{"build:go.sum@web"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := tmp(t)
			for _, f := range c.files {
				write(t, root, f, "x")
			}
			write(t, root, "Dockerfile", "FROM scratch")
			write(t, root, "adapter.yml", "x")
			write(t, root, "src/main.go", "x") // source: never watched
			es, err := WatchSet(Inputs{
				Checkout: root, ComposeJSON: composeJSON(root), PackageManager: c.pm, OtherManifests: c.others,
				ComposeFiles: []string{filepath.Join(root, "docker-compose.yml")}, AdapterFiles: []string{"adapter.yml"},
				MigrationDirs: []string{"prisma/migrations"},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string{
				"build:Dockerfile@web", "build:.dockerignore@web", "compose:docker-compose.yml", "compose:adapter.yml",
				"migrations:prisma/migrations", "plan:.stackfork/knowledge.json",
			}, c.want...)
			sort.Strings(want)
			if got := rels(es); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("got  %v\nwant %v", got, want)
			}
		})
	}
}

func TestWatchSetNoBuildContributesNothing(t *testing.T) {
	root := tmp(t)
	es, err := WatchSet(Inputs{Checkout: root, ComposeJSON: []byte(`{"services":{"db":{"image":"postgres"}}}`), PackageManager: "bun"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rels(es); len(got) != 1 || got[0] != "plan:.stackfork/knowledge.json" {
		t.Fatalf("got %v", got)
	}
}

func TestWatchSetBuildContextDockerfile(t *testing.T) {
	root := tmp(t)
	cj := fmt.Sprintf(`{"services":{"web":{"build":{"context":%q,"dockerfile":"dev.Dockerfile"}}}}`, filepath.Join(root, "apps", "web"))
	write(t, root, "apps/web/bun.lock", "x")
	es, err := WatchSet(Inputs{Checkout: root, ComposeJSON: []byte(cj), PackageManager: "bun"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rels(es), ",")
	for _, w := range []string{"build:apps/web/dev.Dockerfile@web", "build:apps/web/.dockerignore@web", "build:apps/web/bun.lock@web"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %s in %s", w, got)
		}
	}
}

func TestWatchSetOverrides(t *testing.T) {
	root := tmp(t)
	write(t, root, "package.json", "x")
	write(t, root, "infra/web.Dockerfile", "x")
	in := Inputs{Checkout: root, ComposeJSON: composeJSON(root), PackageManager: "bun",
		Overrides: Overrides{
			Watch:  []Override{{Path: "infra/web.Dockerfile", Action: "rebuild", Service: "web"}, {Path: "db/schema", Action: "migrate"}},
			Ignore: []string{"Dockerfile"},
		}}
	es, err := WatchSet(in)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rels(es), ",")
	if !strings.Contains(got, "build:infra/web.Dockerfile@web") || !strings.Contains(got, "migrations:db/schema") {
		t.Errorf("overrides missing: %s", got)
	}
	if strings.Contains(got, "build:Dockerfile@web") {
		t.Errorf("ignored default still watched: %s", got)
	}
	in.Overrides.Watch = []Override{{Path: "x", Action: "rebuild", Service: "worker"}}
	if _, err := WatchSet(in); err == nil || !strings.Contains(err.Error(), `"worker"`) {
		t.Fatalf("unknown service: %v", err)
	}
}

func TestWatchSetRefusesPathOutsideCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	root, outside := tmp(t), tmp(t)
	write(t, outside, "Dockerfile", "x")
	if err := os.Symlink(outside, filepath.Join(root, "shared")); err != nil {
		t.Fatal(err)
	}
	in := Inputs{Checkout: root, Overrides: Overrides{Watch: []Override{{Path: "shared/Dockerfile", Action: "prepare"}}}}
	if _, err := WatchSet(in); err == nil || !strings.Contains(err.Error(), "outside the checkout") {
		t.Fatalf("symlink out: %v", err)
	}
	in = Inputs{Checkout: root, MigrationDirs: []string{"shared"}}
	if _, err := WatchSet(in); err == nil {
		t.Fatal("migration dir through a link out was accepted")
	}
	in = Inputs{Checkout: root, Overrides: Overrides{Watch: []Override{{Path: "../x", Action: "prepare"}}}}
	if _, err := WatchSet(in); err == nil {
		t.Fatal(".. accepted")
	}
}

func TestWatchSetCapNamesLargestDir(t *testing.T) {
	root := tmp(t)
	for i := 0; i < MaxFiles+1; i++ {
		write(t, root, fmt.Sprintf("db/migrate/%d.sql", i), "x")
	}
	write(t, root, "small/a.sql", "x")
	_, err := WatchSet(Inputs{Checkout: root, MigrationDirs: []string{"small", "db/migrate"}})
	if err == nil || !strings.Contains(err.Error(), "db/migrate") || !strings.Contains(err.Error(), "limit 2000") {
		t.Fatalf("got %v", err)
	}
}
