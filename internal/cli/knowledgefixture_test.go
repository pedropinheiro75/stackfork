package cli

import (
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func verifiedFixture(k *knowledge.Knowledge) *knowledge.Knowledge {
	if k.Status == knowledge.StatusVerified {
		if k.Verification == nil {
			k.Verification = &knowledge.Verification{}
		}
		k.Verification.RecipeHash = k.Plan.ExecutionHash()
		k.Verification.RunID = 1
		k.Verification.Mode = knowledge.ModeContainer
	}
	return k
}

func recordVerifyRun(t *testing.T, root string, flags []string) {
	t.Helper()
	newBase, legacyBase := "", ""
	for index, flag := range flags {
		if index+1 < len(flags) {
			switch flag {
			case "--state-dir":
				newBase = flags[index+1]
			case "--legacy-state-dir":
				legacyBase = flags[index+1]
			}
		}
	}
	if newBase == "" {
		newBase, _ = identity.StateDir()
		legacyBase, _ = identity.LegacyStateDir()
	}
	top, project, dir, manifest, err := checkoutManifest(root, newBase, legacyBase)
	if err != nil || manifest == nil {
		t.Fatalf("prepared fixture: %v", err)
	}
	k, err := knowledge.Load(knowledgePath(root))
	if err != nil {
		t.Fatal(err)
	}
	recordPrepareRecipe(&k.Plan, top, manifest)
	rec, err := runs.Begin(dir, runs.Meta{Command: "start-env", Project: project, Mode: knowledge.ModeContainer, RecipeHash: k.Plan.ExecutionHash()})
	if err != nil {
		t.Fatal(err)
	}
	rec.SetSnapshotHash(manifest.ConfigSHA256)
	for _, task := range k.Plan.Steps {
		if task.Once == knowledge.OnceDeps || task.Once == knowledge.OnceData {
			recorded := rec.Step(task.ID, task.Kind, task.Cmd)
			recorded.SetDefinition(task.ExecutionHash(), "")
			recorded.Start()
			recorded.End(nil, 0)
		}
	}
	rec.Finish(nil)
}
