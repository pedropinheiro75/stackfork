package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// shellEval runs cmd through a real /bin/sh, proving a shellQuote'd value
// is genuinely safe for eval "$(stackfork env)" - not just "looks escaped".
func shellEval(t *testing.T, cmd string) string {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c", cmd).Output()
	if err != nil {
		t.Fatalf("sh -c %q: %v", cmd, err)
	}
	return string(out)
}

// envPSLine is docker ps --all --format json's shape for one running
// container (inventory.listContainers' input), with a real published port
// so ConnectionURL actually resolves - gcPSLine's sibling, with ports.
func envPSLine(project, service, port string) string {
	return `{"Labels":"io.stackfork.root=/whatever,com.docker.compose.project=` + project +
		`,com.docker.compose.service=` + service + `","State":"running","Status":"","Ports":"127.0.0.1:` + port + `->5432/tcp"}`
}

// envSetup prepares and (fake-)starts a single-service Postgres instance,
// returning its project name and the JSON docker-ps line env's own
// inventory.Collect call needs to see it as running.
func envSetup(t *testing.T, compose string) (root, stateDir, project, psLine string) {
	t.Helper()
	installFakeDocker(t)
	root = initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir = filepath.Join(t.TempDir(), "state")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, out)
	}
	project, _ = prepared["project"].(string)
	if project == "" {
		t.Fatal("expected a project name from prepare")
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "5"); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	return root, stateDir, project, envPSLine(project, "db", "20050")
}

const envFixturePlaceholder = `services:
  db:
    image: postgres:17
    ports:
      - "5432:5432"
`

func TestEnvDefaultUsesPlaceholder(t *testing.T) {
	root, stateDir, _, psLine := envSetup(t, envFixturePlaceholder)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "env")
	if code != 0 {
		t.Fatalf("env failed: %s", stderr)
	}
	want := `export DATABASE_URL='postgres://postgres:<password>@127.0.0.1:20050/postgres'` + "\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestEnvFormatDotenv(t *testing.T) {
	root, stateDir, _, psLine := envSetup(t, envFixturePlaceholder)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "env", "--format", "dotenv")
	if code != 0 {
		t.Fatalf("env failed: %s", stderr)
	}
	want := `DATABASE_URL="postgres://postgres:<password>@127.0.0.1:20050/postgres"` + "\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestEnvRefusesWhenNotRunning(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(envFixturePlaceholder), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	// Never brought up: no docker-ps line for it at all.
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "env")
	if code == 0 || !strings.Contains(stderr, "not running") {
		t.Fatalf("expected a refusal naming the instance as not running, got code=%d stderr=%s", code, stderr)
	}
}

// TestEnvWithPasswordsEscapesHostileLiteralValue: a password that is a
// literal Compose value (never --secret/env_file/${VAR}) comes back real
// under --with-passwords, single-quote-escaped so eval "$(stackfork env)"
// stays safe even when the value itself contains a single quote.
func TestEnvWithPasswordsEscapesHostileLiteralValue(t *testing.T) {
	// A literal single quote, to prove shellQuote's own escaping; a literal
	// $ has to be written $$ in the source (Compose's own escape for "a
	// literal dollar sign, not the start of interpolation" - compose.json
	// itself keeps it doubled, verified directly, so that's the actual
	// literal value stackfork resolves too, not something this tool
	// un-escapes).
	hostile := `p'a$$(id)word`
	fixture := fmt.Sprintf(`services:
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: %q
    ports:
      - "5432:5432"
`, hostile)
	root, stateDir, _, psLine := envSetup(t, fixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "env", "--with-passwords")
	if code != 0 {
		t.Fatalf("env --with-passwords failed: %s", stderr)
	}
	wantValue := "postgres://postgres:" + hostile + "@127.0.0.1:20050/postgres"
	wantLine := "export DATABASE_URL=" + shellQuote(wantValue) + "\n"
	if out != wantLine {
		t.Fatalf("got:\n%q\nwant:\n%q", out, wantLine)
	}

	// The escaped form must itself be a value eval would accept literally -
	// prove it by having the shell echo it back and comparing.
	echoed := shellEval(t, "printf '%s' "+shellQuote(wantValue))
	if echoed != wantValue {
		t.Fatalf("shell-quoted value round-tripped wrong: got %q want %q", echoed, wantValue)
	}
}

// TestEnvWithoutPasswordsFlagNeverLeaksLiteralPassword: the default
// (placeholder) output must never contain a real literal password value,
// even though --with-passwords proves the value is resolvable.
func TestEnvWithoutPasswordsFlagNeverLeaksLiteralPassword(t *testing.T) {
	root, stateDir, _, psLine := envSetup(t, `services:
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: canary-literal-9f8e7d3c
    ports:
      - "5432:5432"
`)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "env")
	if code != 0 {
		t.Fatalf("env failed: %s", stderr)
	}
	if strings.Contains(out, "canary-literal-9f8e7d3c") {
		t.Fatalf("default env output leaked the literal password:\n%s", out)
	}
	if !strings.Contains(out, "<password>") {
		t.Fatalf("expected the placeholder in default output:\n%s", out)
	}
}

// TestEnvNeverLeaksInterpolatedSecret is the canary: a secret only ever
// present as ${VAR} in the source file (never a literal Compose value)
// must never appear in `stackfork env --with-passwords`'s output, however
// the canary var happens to be set in this test process's own shell -
// render's own environment scoping (design.md D6) already keeps this from
// resolving; this test proves the whole `env` path never regresses it.
func TestEnvNeverLeaksInterpolatedSecret(t *testing.T) {
	const canary = "canary-should-never-appear-1a2b3c"
	t.Setenv("SF_TEST_ENV_CANARY", canary)
	root, stateDir, _, psLine := envSetup(t, `services:
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: ${SF_TEST_ENV_CANARY}
    ports:
      - "5432:5432"
`)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")

	for _, args := range [][]string{
		{"--root", root, "--state-dir", stateDir, "env"},
		{"--root", root, "--state-dir", stateDir, "env", "--with-passwords"},
		{"--root", root, "--state-dir", stateDir, "env", "--format", "dotenv", "--with-passwords"},
	} {
		out, stderr, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("env %v failed: %s", args, stderr)
		}
		if strings.Contains(out, canary) || strings.Contains(stderr, canary) {
			t.Fatalf("env %v leaked the canary:\nstdout=%s\nstderr=%s", args, out, stderr)
		}
	}
}
