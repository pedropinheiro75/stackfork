package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
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
	if isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "learn", "learn | learn record answer|gotcha|recipe|step|secret|live-watch|live-ignore|fact ... | learn verify --check ... --confirm ...")
		return nil
	}
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
// Plan/AdapterFiles/Verification/Status forward onto the fresh scan -
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
	// A recorded plan is a decision and survives a re-scan; a proposed one
	// is replaced by the fresh proposal.
	if existing.Plan.Status == knowledge.PlanRecorded {
		fresh.Plan = existing.Plan
	}
	fresh.Secrets = existing.Secrets
	fresh.Live = existing.Live
	// A migration directory recorded by hand survives a re-scan.
	fresh.Facts.MigrationDirs = unionSorted(fresh.Facts.MigrationDirs, existing.Facts.MigrationDirs)
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
		return errors.New("learn record requires a kind (answer, gotcha, recipe, step, secret, live-watch, live-ignore, fact)")
	}
	path := knowledgePath(root)
	k, err := knowledge.Load(path)
	if err != nil {
		return fmt.Errorf("no knowledge to record into; run `stackfork learn` first: %w", err)
	}

	kind, rest := subArgs[0], subArgs[1:]
	switch kind {
	case "answer":
		if isHelpRequest(rest) {
			printFlaglessUsage(stdout, "learn record answer", "learn record answer <question> <answer>")
			return nil
		}
		if len(rest) != 2 {
			return errors.New("usage: learn record answer <question> <answer>")
		}
		k.Answers = append(k.Answers, knowledge.Answer{Question: rest[0], Answer: rest[1]})
	case "gotcha":
		if isHelpRequest(rest) {
			printFlaglessUsage(stdout, "learn record gotcha", "learn record gotcha <problem> <fix>")
			return nil
		}
		if len(rest) != 2 {
			return errors.New("usage: learn record gotcha <problem> <fix>")
		}
		k.Gotchas = append(k.Gotchas, knowledge.Gotcha{Problem: rest[0], Fix: rest[1]})
	case "recipe":
		fs := newFlagSet(stdout, "learn record recipe")
		prepare := fs.String("prepare", "", "")
		up := fs.String("up", "", "")
		migrate := fs.String("migrate", "", "")
		seedOnce := fs.String("seed-once", "", "")
		seedService := fs.String("seed-service", "", "")
		var login stringList
		fs.Var(&login, "login", "")
		if err := fs.Parse(rest); err != nil {
			return parseFailureErr(fs, err)
		}
		applyRecipeFlags(&k.Plan, *prepare, *up, *migrate, *seedOnce, *seedService, login)
	case "step":
		return cmdLearnRecordStep(stdout, rest, root)
	case "secret":
		if isHelpRequest(rest) {
			printFlaglessUsage(stdout, "learn record secret", "learn record secret NAME [--hint TEXT] | learn record secret --remove NAME")
			return nil
		}
		if err := recordSecret(k, rest); err != nil {
			return err
		}
		if err := learn.WriteKnowledge(root, k); err != nil {
			return err
		}
		return printJSON(stdout, map[string]interface{}{"recorded": "secret", "secrets": k.Secrets})
	case "live-watch", "live-ignore", "fact":
		if isHelpRequest(rest) {
			printFlaglessUsage(stdout, "learn record "+kind, liveRecordUsage[kind])
			return nil
		}
		if err := recordLive(k, kind, rest); err != nil {
			return err
		}
		if err := learn.WriteKnowledge(root, k); err != nil {
			return err
		}
		return printJSON(stdout, map[string]interface{}{"recorded": kind, "live": k.Live, "migration_dirs": k.Facts.MigrationDirs})
	default:
		return fmt.Errorf("unknown learn record kind %q (expected answer, gotcha, recipe, step, secret, live-watch, live-ignore or fact)", kind)
	}

	if err := learn.WriteKnowledge(root, k); err != nil {
		return err
	}
	return printJSON(stdout, map[string]interface{}{
		"recorded": kind, "answers": len(k.Answers), "gotchas": len(k.Gotchas), "plan": k.Plan,
	})
}

// cmdLearnVerify moves knowledge from draft to verified (task 13.6): a
// date, the checks that were actually run, and the user's own confirmation
// quoted verbatim - never self-verified, this is only ever a record of
// what the user said. Refuses unless the plan is recorded and has a
// compose-up step: verifying a plan nobody decided defeats the point.
func cmdLearnVerify(stdout io.Writer, subArgs []string, root string) error {
	fs := newFlagSet(stdout, "learn verify")
	var checks stringList
	fs.Var(&checks, "check", "")
	confirm := fs.String("confirm", "", "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailureErr(fs, err)
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
	if k.Plan.Status != knowledge.PlanRecorded {
		return errors.New("plan is proposed, not recorded; record it with `learn record step` (or `learn record recipe`) before verifying")
	}
	hasUp := false
	for _, st := range k.Plan.Steps {
		hasUp = hasUp || st.Kind == knowledge.KindComposeUp
	}
	if !hasUp {
		return errors.New("plan has no compose-up step; record one with `learn record step --id up --kind compose-up` before verifying")
	}

	k.Status = knowledge.StatusVerified
	k.Verification = &knowledge.Verification{
		Date: time.Now().UTC().Format("2006-01-02"), Checks: checks, Confirmation: *confirm,
	}
	if err := learn.WriteKnowledge(root, k); err != nil {
		return err
	}
	out := map[string]interface{}{"status": k.Status, "verification": k.Verification}
	return printJSON(stdout, out)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyRecipeFlags is the `learn record recipe` alias: it maps the schema 1
// flags onto steps and marks the plan recorded. seedOnce "none" removes the
// data step (a recorded plan without one means never seed).
func applyRecipeFlags(p *knowledge.Plan, prepare, up, migrate, seedOnce, seedService string, login []string) {
	if prepare != "" {
		p.Upsert(knowledge.Step{ID: "prepare", Kind: knowledge.KindPrepare, Args: recipePrepareArgs(prepare)}, "")
	}
	if up != "" {
		has := false
		for _, s := range p.Steps {
			has = has || s.Kind == knowledge.KindComposeUp
		}
		if !has {
			p.Upsert(knowledge.Step{ID: "up", Kind: knowledge.KindComposeUp}, "")
		}
	}
	if migrate != "" {
		st := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema}
		if m := p.MigrateStep(); m != nil {
			st = *m
		}
		st.Cmd = migrate
		p.Upsert(st, "")
	}
	if seedOnce != "" {
		for s := p.SeedStep(); s != nil; s = p.SeedStep() {
			p.Remove(s.ID)
		}
		p.NoSeed = seedOnce == learn.SeedNone
		if !p.NoSeed {
			p.Upsert(knowledge.Step{ID: "seed", Kind: knowledge.KindExec, Cmd: seedOnce, Once: knowledge.OnceData, Service: seedService}, "")
		}
	} else if seedService != "" {
		if s := p.SeedStep(); s != nil {
			s.Service = seedService
			p.Upsert(*s, "")
		}
	}
	if len(login) > 0 {
		var keep []knowledge.Step
		for _, s := range p.Steps {
			if !(s.Kind == knowledge.KindNote && strings.HasPrefix(s.ID, "login")) {
				keep = append(keep, s)
			}
		}
		p.Steps = keep
		for i, l := range login {
			id := "login"
			if i > 0 {
				id = fmt.Sprintf("login-%d", i+1)
			}
			p.Upsert(knowledge.Step{ID: id, Kind: knowledge.KindNote, Text: l}, "")
		}
	}
	p.Status = knowledge.PlanRecorded
}

// cmdLearnRecordStep is `learn record step`: upsert one step by id (or
// remove one with --remove) and mark the plan recorded.
func cmdLearnRecordStep(stdout io.Writer, rest []string, root string) error {
	fs := newFlagSet(stdout, "learn record step")
	id := fs.String("id", "", "")
	kind := fs.String("kind", "", "")
	cmd := fs.String("cmd", "", "")
	service := fs.String("service", "", "")
	once := fs.String("once", "", "")
	modes := fs.String("modes", "", "")
	exclude := fs.String("exclude", "", "")
	name := fs.String("name", "", "")
	dir := fs.String("dir", "", "")
	portEnv := fs.String("port-env", "", "")
	port := fs.Int("port", 0, "")
	args := fs.String("args", "", "")
	text := fs.String("text", "", "")
	before := fs.String("before", "", "")
	remove := fs.String("remove", "", "")
	if err := fs.Parse(rest); err != nil {
		return parseFailureErr(fs, err)
	}
	path := knowledgePath(root)
	k, err := knowledge.Load(path)
	if err != nil {
		return fmt.Errorf("no knowledge to record into; run `stackfork learn` first: %w", err)
	}
	if *remove != "" {
		if !k.Plan.Remove(*remove) {
			return fmt.Errorf("no step %q in the plan", *remove)
		}
	} else {
		if *id == "" || *kind == "" {
			return errors.New("usage: learn record step --id ID --kind K [--cmd C] [--service S] [--once data|schema|none] [--modes a,b] [--exclude a,b] [--name N] [--dir D] [--port-env A,B] [--port N] [--args A,B] [--text T] [--before ID] | --remove ID")
		}
		st := knowledge.Step{ID: *id, Kind: *kind, Cmd: *cmd, Service: *service, Once: *once,
			Modes: splitList(*modes), Exclude: splitList(*exclude), Name: *name, Dir: *dir,
			PortEnv: splitList(*portEnv), Port: *port, Args: splitList(*args), Text: *text}
		if len(st.Args) > 0 && st.Kind != knowledge.KindPrepare {
			return errors.New("--args is for prepare steps only")
		}
		prev := stepByID(k.Plan, st.ID)
		switch {
		case st.Once == "none" && prev != nil && prev.Once == knowledge.OnceData:
			// "never seed" is an explicit answer, not just a removed step.
			k.Plan.Remove(st.ID)
			k.Plan.NoSeed = true
		default:
			if st.Once == "none" {
				st.Once = ""
			}
			if st.Kind == knowledge.KindExec && st.Once == knowledge.OnceData {
				k.Plan.NoSeed = false
			}
			k.Plan.Upsert(st, *before)
		}
	}
	k.Plan.Status = knowledge.PlanRecorded
	if err := knowledge.ValidatePlan(k.Plan); err != nil {
		return err
	}
	if err := learn.WriteKnowledge(root, k); err != nil {
		return err
	}
	return printJSON(stdout, map[string]interface{}{"recorded": "step", "plan": k.Plan})
}

func stepByID(p knowledge.Plan, id string) *knowledge.Step {
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i]
		}
	}
	return nil
}

// recordSecret declares (or, with --remove, drops) a secret NAME in k. Knowledge
// holds names and hints only; there is no flag that takes a reference or a
// value. NAME and the flags may come in either order.
func recordSecret(k *knowledge.Knowledge, rest []string) error {
	fs := newFlagSet(io.Discard, "learn record secret")
	hint := fs.String("hint", "", "")
	remove := fs.String("remove", "", "")
	var names []string
	for {
		if err := fs.Parse(rest); err != nil {
			return parseFailureErr(fs, err)
		}
		if fs.NArg() == 0 {
			break
		}
		names = append(names, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if *remove != "" {
		if len(names) > 0 || *hint != "" {
			return errors.New("usage: learn record secret --remove NAME")
		}
		if !k.RemoveSecret(*remove) {
			if !compose.ValidSecretName(*remove) {
				return errors.New("no such secret declared")
			}
			return fmt.Errorf("no secret %q is declared", *remove)
		}
		return nil
	}
	if len(names) != 1 {
		return errors.New("usage: learn record secret NAME [--hint TEXT] | learn record secret --remove NAME")
	}
	k.UpsertSecret(names[0], *hint)
	return knowledge.ValidateSecrets(k.Secrets)
}

var liveRecordUsage = map[string]string{
	"live-watch":  "learn record live-watch PATH --action rebuild|prepare|migrate [--service S] | learn record live-watch --remove PATH",
	"live-ignore": "learn record live-ignore PATH | learn record live-ignore --remove PATH",
	"fact":        "learn record fact migration-dir PATH | learn record fact migration-dir --remove PATH",
}

func unionSorted(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range [][]string{a, b} {
		for _, s := range l {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// recordLive handles live-watch, live-ignore and fact migration-dir. Flags
// and the PATH may come in either order. Errors never echo the path.
func recordLive(k *knowledge.Knowledge, kind string, rest []string) error {
	usage := liveRecordUsage[kind]
	if kind == "fact" {
		if len(rest) == 0 || rest[0] != "migration-dir" {
			return errors.New("usage: " + usage)
		}
		rest = rest[1:]
	}
	fs := newFlagSet(io.Discard, "learn record "+kind)
	action := fs.String("action", "", "")
	service := fs.String("service", "", "")
	remove := fs.Bool("remove", false, "")
	var paths []string
	for {
		if err := fs.Parse(rest); err != nil {
			return parseFailureErr(fs, err)
		}
		if fs.NArg() == 0 {
			break
		}
		paths = append(paths, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(paths) != 1 {
		return errors.New("usage: " + usage)
	}
	p := paths[0]
	if kind != "live-watch" && (*action != "" || *service != "") {
		return errors.New("usage: " + usage)
	}
	if err := knowledge.ValidateLivePath(p); err != nil {
		return err
	}
	if *remove {
		if kind == "live-watch" && (*action != "" || *service != "") {
			return errors.New("usage: " + usage)
		}
		var found bool
		switch kind {
		case "live-watch":
			found = k.RemoveLiveWatch(p)
		case "live-ignore":
			found = k.RemoveLiveIgnore(p)
		default:
			found = k.RemoveMigrationDir(p)
		}
		if !found {
			return errors.New("nothing recorded for that path")
		}
		return nil
	}
	switch kind {
	case "live-watch":
		return k.UpsertLiveWatch(knowledge.LiveWatch{Path: p, Action: *action, Service: *service})
	case "live-ignore":
		return k.AddLiveIgnore(p)
	default:
		k.AddMigrationDir(p)
		return nil
	}
}
