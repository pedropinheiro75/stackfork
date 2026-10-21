package dashboard_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// A card's Start that does not go through StartEnv (planRunsHost and PlanMode
// disagree, as in a checkout whose main repo cannot be told) still never runs
// an untrusted host seed: the run itself checks trust.
func TestDirectStartRefusesAnUntrustedHostSeed(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1") // no data yet: the up creates it and seeds
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	compose := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(compose, []byte("services:\n  db:\n    image: postgres:17\n    volumes:\n      - data:/var/lib/postgresql/data\nvolumes:\n  data: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
			{ID: "up", Kind: knowledge.KindComposeUp},
			{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnHost, Once: knowledge.OnceData, Cmd: "bun run db:seed"},
		}}}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	newBase, legacyBase := t.TempDir(), t.TempDir()
	var out, errBuf bytes.Buffer
	if code := cli.Run([]string{"--root", root, "--state-dir", newBase, "prepare", "-f", compose}, &out, &errBuf); code != 0 {
		t.Fatalf("prepare: %s", errBuf.String())
	}
	var prepared struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal(out.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	var calls []string
	old := lifecycle.HostExec
	lifecycle.HostExec = func(w io.Writer, _, _, _, _, _ string, s knowledge.Step) (int, error) {
		calls = append(calls, s.Cmd)
		return 0, nil
	}
	t.Cleanup(func() { lifecycle.HostExec = old })

	srv, err := dashboard.New(newBase, legacyBase, "test", 55341)
	if err != nil {
		t.Fatal(err)
	}
	srv.StartEnv = nil // the direct path
	postAction(t, srv, "127.0.0.1:55341", actionRequestFixture{Action: "up", Project: prepared.Project})
	if len(calls) != 0 {
		t.Fatalf("an untrusted host seed ran: %v", calls)
	}
}
