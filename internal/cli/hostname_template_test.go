package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestConfigSetHostname(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	var out bytes.Buffer
	if _, err := cmdConfig(&out, []string{"set", "hostname", "{worktree}-{hash}"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, err := cmdConfig(&out, nil); err != nil || !strings.Contains(out.String(), `"hostname": "{worktree}-{hash}"`) {
		t.Fatalf("got %s, %v", out.String(), err)
	}
	for _, bad := range []string{"{branch}", "a.b", "UPPER"} {
		_, err := cmdConfig(&out, []string{"set", "hostname", bad})
		if err == nil || !strings.Contains(err.Error(), "{app}, {worktree} and {hash}") {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func writeHostnameFixture(t *testing.T, base, project, hostname string) {
	t.Helper()
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{Project: project, Root: "/x", Branch: "main", Hostname: hostname}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
}

func TestChooseHostname(t *testing.T) {
	base, legacy := t.TempDir(), t.TempDir()
	root := filepath.Join(t.TempDir(), "acme-web")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	project, _ := identity.ProjectName(root, "main")
	choose := func(flag, saved, tmpl string) (string, string) {
		var errb bytes.Buffer
		got, err := chooseHostname(&errb, flag, saved, tmpl, root, "main", project, base, legacy)
		if err != nil {
			t.Fatal(err)
		}
		return got, errb.String()
	}
	_, hash, _ := identity.TemplateHostname("", root, "main")

	if got, note := choose("", "", ""); got != "acme-web.localhost" || note != "" {
		t.Fatalf("default: %s %q", got, note)
	}
	// a re-prepare keeps the saved name after the template changed
	if got, _ := choose("", "acme-web.localhost", "stackfork-{app}-{worktree}"); got != "acme-web.localhost" {
		t.Fatalf("saved: %s", got)
	}
	// the flag wins over the saved name
	if got, _ := choose("web-274.localhost", "acme-web.localhost", ""); got != "web-274.localhost" {
		t.Fatalf("flag: %s", got)
	}
	// the reserved name gets the suffix, with a note
	got, note := choose("", "", "stackfork")
	if want := "stackfork-" + hash[:6] + ".localhost"; got != want || !strings.Contains(note, "stackfork.localhost is taken") {
		t.Fatalf("reserved: %s %q", got, note)
	}
	// a name held by another instance (current or legacy state dir) gets the suffix
	writeHostnameFixture(t, base, "sf-other-aaaaaaaaaaaa", "acme-web.localhost")
	if got, note := choose("", "", ""); got != "acme-web-"+hash[:6]+".localhost" || note == "" {
		t.Fatalf("held: %s %q", got, note)
	}
	writeHostnameFixture(t, legacy, "idv-other-bbbbbbbbbbbb", "feature-x.localhost")
	if got, _ := choose("", "", "feature-x"); got != "feature-x-"+hash[:6]+".localhost" {
		t.Fatalf("legacy held: %s", got)
	}
	// this instance's own saved name never counts as taken
	writeHostnameFixture(t, base, project, "acme-web.localhost")
	writeHostnameFixture(t, base, "sf-other-aaaaaaaaaaaa", "elsewhere.localhost")
	if got, _ := choose("", "", ""); got != "acme-web.localhost" {
		t.Fatalf("own: %s", got)
	}
}
