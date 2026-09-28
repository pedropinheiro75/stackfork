package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
)

// learnOutput summarises a scan; never includes an env value, a script's
// full command text where it might quote one, or an env file's contents -
// only counts and the paths written.
type learnOutput struct {
	Root         string   `json:"root"`
	Status       string   `json:"status"`
	Resumed      bool     `json:"resumed,omitempty"`
	ChangedFacts []string `json:"changed_facts,omitempty"`
	ComposeFiles int      `json:"compose_files"`
	Services     int      `json:"services"`
	Refusals     int      `json:"isolation_findings"`
	Risks        int      `json:"risks"`
	EnvKeys      int      `json:"env_keys"`
	Written      string   `json:"written"`
}

// cmdLearn dispatches the bare scan and the record/verify subcommands
// (task 13.5/13.6) added on top of it.
func cmdLearn(stdout io.Writer, subArgs []string, root string) error {
	if len(subArgs) == 0 {
		return cmdLearnScan(stdout, root)
	}
	switch subArgs[0] {
	case "record":
		return cmdLearnRecord(stdout, subArgs[1:], root)
	case "verify":
		return cmdLearnVerify(stdout, subArgs[1:], root)
	default:
		return fmt.Errorf("unknown learn subcommand %q (expected record or verify)", subArgs[0])
	}
}

func knowledgePath(root string) string {
	return filepath.Join(root, ".stackfork", "knowledge.json")
}

// cmdLearnScan runs the deterministic scan (task 13.2) and writes
// .stackfork/knowledge.json, .stackfork/knowledge.md and
// .stackfork/.gitignore. Resuming a draft, or re-scanning stale knowledge
// (task 13.5/13.7), carries the existing file's recorded Answers/Gotchas/
// Recipe/AdapterFiles/Verification/Status forward onto the fresh scan -
// only Facts/Refusals/Risks/EnvKeys/Inputs are ever replaced, since those
// are exactly what the scan is authoritative for; a session's own
// progress must never be silently discarded by re-running learn.
func cmdLearnScan(stdout io.Writer, root string) error {
	fresh, err := learn.Scan(root)
	if err != nil {
		return err
	}
	changed, resumed := mergeKnowledgeFile(root, knowledgePath(root), fresh)
	if err := learn.WriteKnowledge(root, fresh); err != nil {
		return err
	}
	return printJSON(stdout, learnOutput{
		Root: root, Status: fresh.Status, Resumed: resumed, ChangedFacts: changed,
		ComposeFiles: len(fresh.Facts.ComposeFiles),
		Services:     len(fresh.Facts.Services),
		Refusals:     len(fresh.Refusals),
		Risks:        len(fresh.Risks),
		EnvKeys:      len(fresh.EnvKeys),
		Written:      filepath.Join(root, ".stackfork"),
	})
}

// mergeKnowledgeFile loads any existing knowledge.json at path; if one
// exists, changedInputs is its own StaleInputs() computed before fresh
// overwrites anything (task 13.7: "lists only the changed facts"), and its
// session progress is carried onto fresh in place.
func mergeKnowledgeFile(root, path string, fresh *knowledge.Knowledge) (changedInputs []string, resumed bool) {
	existing, err := knowledge.Load(path)
	if err != nil || existing == nil {
		return nil, false
	}
	changedInputs = existing.StaleInputs(root)
	fresh.Status = existing.Status
	fresh.AdapterFiles = existing.AdapterFiles
	fresh.Recipe = existing.Recipe
	fresh.Answers = existing.Answers
	fresh.Gotchas = existing.Gotchas
	fresh.Verification = existing.Verification
	return changedInputs, true
}

// cmdLearnRecord appends one answer/gotcha, or sets recipe fields, into an
// existing draft (task 13.5). There is nothing to resume into without a
// prior `stackfork learn`.
func cmdLearnRecord(stdout io.Writer, subArgs []string, root string) error {
	if len(subArgs) == 0 {
		return errors.New("learn record requires a kind (answer, gotcha, recipe)")
	}
	path := knowledgePath(root)
	k, err := knowledge.Load(path)
	if err != nil {
		return fmt.Errorf("no knowledge to record into; run `stackfork learn` first: %w", err)
	}

	kind, rest := subArgs[0], subArgs[1:]
	switch kind {
	case "answer":
		if len(rest) != 2 {
			return errors.New("usage: learn record answer <question> <answer>")
		}
		k.Answers = append(k.Answers, knowledge.Answer{Question: rest[0], Answer: rest[1]})
	case "gotcha":
		if len(rest) != 2 {
			return errors.New("usage: learn record gotcha <problem> <fix>")
		}
		k.Gotchas = append(k.Gotchas, knowledge.Gotcha{Problem: rest[0], Fix: rest[1]})
	case "recipe":
		fs := flag.NewFlagSet("learn record recipe", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		prepare := fs.String("prepare", "", "")
		up := fs.String("up", "", "")
		migrate := fs.String("migrate", "", "")
		seedOnce := fs.String("seed-once", "", "")
		var login stringList
		fs.Var(&login, "login", "")
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%s", err.Error())
		}
		if *prepare != "" {
			k.Recipe.Prepare = *prepare
		}
		if *up != "" {
			k.Recipe.Up = *up
		}
		if *migrate != "" {
			k.Recipe.Migrate = *migrate
		}
		if *seedOnce != "" {
			k.Recipe.SeedOnce = *seedOnce
		}
		if len(login) > 0 {
			k.Recipe.Login = login
		}
	default:
		return fmt.Errorf("unknown learn record kind %q (expected answer, gotcha or recipe)", kind)
	}

	if err := learn.WriteKnowledge(root, k); err != nil {
		return err
	}
	return printJSON(stdout, map[string]interface{}{
		"recorded": kind, "answers": len(k.Answers), "gotchas": len(k.Gotchas), "recipe": k.Recipe,
	})
}

// cmdLearnVerify moves knowledge from draft to verified (task 13.6): a
// date, the checks that were actually run, and the user's own confirmation
// quoted verbatim - never self-verified, this is only ever a record of
// what the user said. Refuses if the recipe's own core steps (prepare, up)
// were never recorded: verifying a recipe nobody wrote down defeats the
// point of recording one.
func cmdLearnVerify(stdout io.Writer, subArgs []string, root string) error {
	fs := flag.NewFlagSet("learn verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var checks stringList
	fs.Var(&checks, "check", "")
	confirm := fs.String("confirm", "", "")
	if err := fs.Parse(subArgs); err != nil {
		return fmt.Errorf("%s", err.Error())
	}
	if len(checks) == 0 {
		return errors.New("at least one --check is required")
	}
	if *confirm == "" {
		return errors.New("--confirm is required: the user's own words confirming the app works")
	}

	path := knowledgePath(root)
	k, err := knowledge.Load(path)
	if err != nil {
		return fmt.Errorf("no knowledge to verify; run `stackfork learn` first: %w", err)
	}
	if k.Recipe.Prepare == "" || k.Recipe.Up == "" {
		return errors.New("recipe is incomplete (prepare/up not recorded); run `learn record recipe` before verifying")
	}

	k.Status = knowledge.StatusVerified
	k.Verification = &knowledge.Verification{
		Date: time.Now().UTC().Format("2006-01-02"), Checks: checks, Confirmation: *confirm,
	}
	if err := learn.WriteKnowledge(root, k); err != nil {
		return err
	}
	return printJSON(stdout, map[string]interface{}{"status": k.Status, "verification": k.Verification})
}
