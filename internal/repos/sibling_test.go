package repos

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

const canary = "CANARY-s3cr3t-value"

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// siblingFixture: main checkout with a tracked docker-compose.yml, a
// sibling worktree with an untracked .agent-dev adapter + runtime.env, and
// an instance record for the sibling.
func siblingFixture(t *testing.T) (main, sib string, inst inventory.Instance) {
	t.Helper()
	main = mkRepo(t, filepath.Join(tempRoot(t), "proj"))
	write(t, filepath.Join(main, "docker-compose.yml"), "services: {app: {image: x}}\n", 0o644)
	git(t, main, "add", ".")
	git(t, main, "commit", "-q", "-m", "compose")
	res, err := AddWorktree(main, "feat-a", "main")
	if err != nil {
		t.Fatal(err)
	}
	sib = res.Path
	ad := filepath.Join(sib, ".agent-dev")
	write(t, filepath.Join(ad, "compose.yml"), "services: {app: {env_file: ./.agent-dev/runtime.env}}\n", 0o644)
	write(t, filepath.Join(ad, "start.sh"), "#!/bin/sh\necho hi\n", 0o755)
	write(t, filepath.Join(ad, "runtime.env"),
		"DEV_CHECKOUT="+sib+"\nDEV_RUNTIME_ENV="+sib+"/.agent-dev/runtime.env\nTOKENISH="+canary+"\nLOGDIR=\""+sib+"/logs\"\nOTHER=/elsewhere\n", 0o600)
	write(t, filepath.Join(ad, "run.log"), "log "+canary, 0o644)
	write(t, filepath.Join(ad, ".env"), "SECRET="+canary, 0o644)
	write(t, filepath.Join(ad, "deploy.pem"), canary, 0o644)
	write(t, filepath.Join(ad, "sub", "deep.txt"), "x", 0o644)
	inst = inventory.Instance{
		Project: "sf-proj-feat-a", Root: sib, Branch: "feat-a", Used: time.Now(),
		Files:   []string{filepath.Join(sib, "docker-compose.yml"), filepath.Join(ad, "compose.yml")},
		EnvFile: filepath.Join(ad, "runtime.env"),
		Secrets: map[string]string{"npm_token": "npmrc:/home/u/.npmrc#npm.pkg.github.com"},
	}
	return
}

func TestSiblingRecipeAppliesToNewWorktree(t *testing.T) {
	main, sib, inst := siblingFixture(t)
	res, err := AddWorktree(main, "feat-b", "main")
	if err != nil {
		t.Fatal(err)
	}
	r, err := FindSiblingRecipe(main, res.Path, "", []inventory.Instance{inst})
	if err != nil || r == nil {
		t.Fatalf("recipe %v err %v", r, err)
	}
	if strings.Join(r.Files, ",") != "docker-compose.yml,.agent-dev/compose.yml" || r.EnvFile != ".agent-dev/runtime.env" {
		t.Fatalf("recipe %+v", r)
	}
	if r.Secrets["npm_token"] != inst.Secrets["npm_token"] {
		t.Fatalf("secrets %v", r.Secrets)
	}
	rep, err := r.Apply(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	ad := filepath.Join(res.Path, ".agent-dev")
	for _, f := range []string{"compose.yml", "start.sh", "runtime.env"} {
		if _, err := os.Stat(filepath.Join(ad, f)); err != nil {
			t.Errorf("%s not copied: %v", f, err)
		}
	}
	for _, f := range []string{".env", "deploy.pem", "run.log", "sub"} {
		if _, err := os.Lstat(filepath.Join(ad, f)); err == nil {
			t.Errorf("%s must not be copied", f)
		}
	}
	env, _ := os.ReadFile(filepath.Join(ad, "runtime.env"))
	for _, want := range []string{"DEV_CHECKOUT=" + res.Path + "\n", "DEV_RUNTIME_ENV=" + filepath.Join(res.Path, ".agent-dev", "runtime.env") + "\n", "TOKENISH=" + canary, `LOGDIR="` + res.Path + `/logs"`, "OTHER=/elsewhere"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("runtime.env missing %q:\n%s", want, env)
		}
	}
	if strings.Contains(string(env), sib) {
		t.Errorf("sibling path left in runtime.env:\n%s", env)
	}
	if fi, _ := os.Stat(filepath.Join(ad, "runtime.env")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("runtime.env mode %v", fi.Mode())
	}
	// the sibling's own file is untouched
	if b, _ := os.ReadFile(filepath.Join(sib, ".agent-dev", "runtime.env")); !strings.Contains(string(b), "DEV_CHECKOUT="+sib) {
		t.Error("sibling runtime.env was modified")
	}
	// the report names things but never leaks a value
	all := strings.Join(append(append(append([]string{}, rep.Copied...), rep.Rewritten...), rep.Skipped...), "\n")
	if strings.Contains(all, canary) || !strings.Contains(all, "DEV_CHECKOUT") || !strings.Contains(all, ".agent-dev/start.sh") {
		t.Errorf("report: %s", all)
	}
	// a second apply changes nothing and does not clobber
	write(t, filepath.Join(ad, "runtime.env"), "EDITED=1\n", 0o600)
	if _, err := r.Apply(res.Path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(ad, "runtime.env")); string(b) != "EDITED=1\n" {
		t.Errorf("existing runtime.env overwritten: %s", b)
	}
}

func TestSiblingRecipeReadinessAndRefusals(t *testing.T) {
	main, sib, inst := siblingFixture(t)
	insts := []inventory.Instance{inst}
	// readiness mode: tracked compose file exists on main
	if r, err := FindSiblingRecipe(main, "", "main", insts); err != nil || r == nil {
		t.Fatalf("want recipe, got %v %v", r, err)
	}
	// a tracked file that exists only on the sibling's branch is not derivable
	write(t, filepath.Join(sib, "extra.yml"), "services: {}\n", 0o644)
	git(t, sib, "add", "extra.yml")
	git(t, sib, "commit", "-q", "-m", "extra")
	inst.Files = append(inst.Files, filepath.Join(sib, "extra.yml"))
	r, err := FindSiblingRecipe(main, "", "main", []inventory.Instance{inst})
	if r != nil || err == nil || !strings.Contains(err.Error(), "extra.yml") {
		t.Fatalf("got %v %v", r, err)
	}
	// unrelated repo's instance, missing sibling dir and own instance are ignored
	other := mkRepo(t, filepath.Join(tempRoot(t), "other"))
	foreign := inventory.Instance{Root: other, Files: []string{filepath.Join(other, "x.yml")}, Used: time.Now()}
	gone := inventory.Instance{Root: filepath.Join(main, ".worktrees", "gone"), Files: []string{"/x"}, Used: time.Now()}
	if r, err := FindSiblingRecipe(main, "", "main", []inventory.Instance{foreign, gone}); r != nil || err != nil {
		t.Fatalf("got %v %v", r, err)
	}
	// a non-runtime env file is never copied
	_, sib2, inst2 := siblingFixture(t)
	inst2.EnvFile = filepath.Join(sib2, ".env.local")
	write(t, inst2.EnvFile, "A=1", 0o600)
	m2 := MainRepoOf(sib2)
	res, _ := AddWorktree(m2, "feat-c", "main")
	if r, err := FindSiblingRecipe(m2, res.Path, "", []inventory.Instance{inst2}); r != nil || err == nil || !strings.Contains(err.Error(), ".env.local") {
		t.Fatalf("got %v %v", r, err)
	}
}

func TestSiblingRecipeMostRecentlyUsedFirst(t *testing.T) {
	main, sib, inst := siblingFixture(t)
	res2, _ := AddWorktree(main, "feat-old", "main")
	old := inventory.Instance{Project: "old", Root: res2.Path, Files: []string{filepath.Join(res2.Path, "docker-compose.yml")}, Used: time.Now().Add(-time.Hour)}
	res3, _ := AddWorktree(main, "feat-new", "main")
	r, err := FindSiblingRecipe(main, res3.Path, "", []inventory.Instance{old, inst})
	if err != nil || r == nil || r.Source != sib || r.Project != inst.Project {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestRewriteEnvHandlesBothMacSpellingsAndBoundaries(t *testing.T) {
	in := "A=/private/var/x/sib\nB=/var/x/sib/sub\nC=/var/x/sib-other\nD='/var/x/sib'\n# DEV_CHECKOUT=/var/x/sib\n"
	out, keys := rewriteEnv([]byte(in), []string{"/private/var/x/sib", "/var/x/sib"}, "/new", "/new/e")
	if string(out) != "A=/new\nB=/new/sub\nC=/var/x/sib-other\nD='/new'\n# DEV_CHECKOUT=/var/x/sib\n" || strings.Join(keys, ",") != "A,B,D" {
		t.Fatalf("%q %v", out, keys)
	}
}

func TestSiblingCopiesReferenceOnlyRegistryConfig(t *testing.T) {
	main, sib, inst := siblingFixture(t)
	write(t, filepath.Join(sib, ".npmrc"), "@rrglobal:registry=https://npm.pkg.github.com\n//npm.pkg.github.com/:_authToken=${NPM_TOKEN}\n", 0o644)
	write(t, filepath.Join(sib, ".yarnrc.yml"), "npmAuthToken: \"${NPM_TOKEN}\"\n", 0o644)
	write(t, filepath.Join(sib, "bunfig.toml"), "[install.scopes]\n\"@a\" = { token = \"$NPM_TOKEN\", url = \"https://x\" }\n", 0o644)
	// literal tokens are never copied, even next to a reference
	write(t, filepath.Join(sib, ".yarnrc"), "npmAuthToken \"ghp_"+canary+"\"\n", 0o644)
	write(t, filepath.Join(sib, ".pnpmrc"), "//r/:_authToken=${A}\n//r2/:_authToken=ghp_"+canary+"\n", 0o644)
	// a tracked registry file is the worktree's own business
	write(t, filepath.Join(main, ".bunfig.toml"), "x=1\n", 0o644)
	res, err := AddWorktree(main, "feat-c", "main")
	if err != nil {
		t.Fatal(err)
	}
	r, err := FindSiblingRecipe(main, res.Path, "", []inventory.Instance{inst})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := r.Apply(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".npmrc", ".yarnrc.yml", "bunfig.toml"} {
		if _, err := os.Stat(filepath.Join(res.Path, f)); err != nil {
			t.Errorf("%s not copied", f)
		}
		if !strings.Contains(strings.Join(rep.Copied, "\n"), f) {
			t.Errorf("%s not reported: %v", f, rep.Copied)
		}
	}
	for _, f := range []string{".yarnrc", ".pnpmrc"} {
		if _, err := os.Lstat(filepath.Join(res.Path, f)); err == nil {
			t.Errorf("%s with a literal token was copied", f)
		}
	}
	all := strings.Join(append(rep.Copied, rep.Skipped...), "\n")
	if strings.Contains(all, canary) {
		t.Errorf("report leaks value: %s", all)
	}
}

func TestSiblingLeavesTrackedRegistryConfigAlone(t *testing.T) {
	main := mkRepo(t, filepath.Join(tempRoot(t), "proj"))
	write(t, filepath.Join(main, "docker-compose.yml"), "services: {app: {image: x}}\n", 0o644)
	write(t, filepath.Join(main, ".npmrc"), "registry=https://tracked.example\n", 0o644)
	git(t, main, "add", ".")
	git(t, main, "commit", "-q", "-m", "c")
	sibRes, _ := AddWorktree(main, "feat-a", "main")
	write(t, filepath.Join(sibRes.Path, ".npmrc"), "registry=https://edited.example\n", 0o644)
	inst := inventory.Instance{Project: "p", Root: sibRes.Path, Used: time.Now(), Files: []string{filepath.Join(sibRes.Path, "docker-compose.yml")}}
	res, _ := AddWorktree(main, "feat-b", "main")
	r, err := FindSiblingRecipe(main, res.Path, "", []inventory.Instance{inst})
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := r.Apply(res.Path)
	if b, _ := os.ReadFile(filepath.Join(res.Path, ".npmrc")); !strings.Contains(string(b), "tracked.example") {
		t.Errorf("tracked .npmrc touched: %s", b)
	}
	if strings.Contains(strings.Join(rep.Copied, ","), ".npmrc") {
		t.Errorf("reported tracked file: %v", rep.Copied)
	}
}

func TestSiblingRecipeCarriesSeedOnce(t *testing.T) {
	main, _, inst := siblingFixture(t)
	inst.SeedOnce, inst.SeedService = "bun run db:seed", "web"
	res, err := AddWorktree(main, "feat-s", "main")
	if err != nil {
		t.Fatal(err)
	}
	r, err := FindSiblingRecipe(main, res.Path, "", []inventory.Instance{inst})
	if err != nil || r.SeedOnce != "bun run db:seed" || r.SeedService != "web" {
		t.Fatalf("%+v %v", r, err)
	}
}
