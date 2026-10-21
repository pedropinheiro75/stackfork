package knowledge

import (
	"fmt"
	"strings"
	"unicode"
)

// Modes a plan can run in (design.md D1/D3).
const (
	ModeContainer = "container"
	ModeHybrid    = "hybrid"
)

// Step kinds: a closed set. wait-http is reserved and refused.
const (
	KindPrepare   = "prepare"
	KindComposeUp = "compose-up"
	KindExec      = "exec"
	KindHost      = "host"
	KindNote      = "note"
)

// KindSecrets is the kind (and id) of the preflight step the runner adds to
// every run. A plan cannot declare it.
const KindSecrets = "secrets"

// Once values for an exec step.
const (
	OnceData   = "data"
	OnceSchema = "schema"
)

// Plan status values.
const (
	PlanProposed = "proposed"
	PlanRecorded = "recorded"
)

// Step is one entry of the startup plan; the fields used depend on Kind.
type Step struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	Modes []string `json:"modes,omitempty"`

	Args    []string `json:"args,omitempty"`    // prepare
	Exclude []string `json:"exclude,omitempty"` // compose-up

	Service string `json:"service,omitempty"` // exec
	Cmd     string `json:"cmd,omitempty"`     // exec, host
	Once    string `json:"once,omitempty"`    // exec

	Name    string   `json:"name,omitempty"` // host
	Dir     string   `json:"dir,omitempty"`
	PortEnv []string `json:"port_env,omitempty"`
	Port    int      `json:"port,omitempty"` // host: the dev server port, when known

	Text string `json:"text,omitempty"` // note
}

// Plan is the ordered startup sequence. Steps run in list order.
type Plan struct {
	Modes      []string `json:"modes,omitempty"`
	AppService string   `json:"app_service,omitempty"`
	Status     string   `json:"status,omitempty"`
	// NoSeed records the explicit answer "never seed". A recorded plan with
	// neither a data step nor NoSeed leaves seeding undecided (auto-detect).
	NoSeed bool   `json:"no_seed,omitempty"`
	Steps  []Step `json:"steps,omitempty"`
}

func (s Step) inMode(mode string) bool {
	if len(s.Modes) == 0 {
		return true
	}
	for _, m := range s.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// ForMode splits the steps into those that run in mode and those skipped.
func (p Plan) ForMode(mode string) (run, skipped []Step) {
	for _, s := range p.Steps {
		if s.inMode(mode) {
			run = append(run, s)
		} else {
			skipped = append(skipped, s)
		}
	}
	return
}

// HostSteps returns the host steps in order.
func (p Plan) HostSteps() []Step {
	var out []Step
	for _, s := range p.Steps {
		if s.Kind == KindHost {
			out = append(out, s)
		}
	}
	return out
}

func (p Plan) firstOnce(once string) *Step {
	for i := range p.Steps {
		if p.Steps[i].Kind == KindExec && p.Steps[i].Once == once {
			s := p.Steps[i]
			return &s
		}
	}
	return nil
}

// SeedStep is the first exec step with once: data, or nil.
func (p Plan) SeedStep() *Step { return p.firstOnce(OnceData) }

// MigrateStep is the first exec step with once: schema, or nil.
func (p Plan) MigrateStep() *Step { return p.firstOnce(OnceSchema) }

// SupportsMode reports whether the plan can run in mode: container always
// (unless Modes excludes it), hybrid only with a host step.
func (p Plan) SupportsMode(mode string) bool {
	if len(p.Modes) > 0 {
		found := false
		for _, m := range p.Modes {
			found = found || m == mode
		}
		if !found {
			return false
		}
	}
	return mode == ModeContainer || (mode == ModeHybrid && len(p.HostSteps()) > 0)
}

// Upsert replaces the step with s.ID in place, or inserts it before the step
// named before (appending when before is empty or unknown). A non-empty
// before also moves an existing step.
func (p *Plan) Upsert(s Step, before string) {
	idx := -1
	for i, x := range p.Steps {
		if x.ID == s.ID {
			idx = i
		}
	}
	if idx >= 0 && before == "" {
		p.Steps[idx] = s
		return
	}
	if idx >= 0 {
		p.Steps = append(p.Steps[:idx], p.Steps[idx+1:]...)
	}
	for i, x := range p.Steps {
		if before != "" && x.ID == before {
			p.Steps = append(p.Steps[:i], append([]Step{s}, p.Steps[i:]...)...)
			return
		}
	}
	p.Steps = append(p.Steps, s)
}

// Remove deletes the step with id; it reports whether one existed.
func (p *Plan) Remove(id string) bool {
	for i, x := range p.Steps {
		if x.ID == id {
			p.Steps = append(p.Steps[:i], p.Steps[i+1:]...)
			return true
		}
	}
	return false
}

// SplitArgv splits a host step's cmd into an argument vector: whitespace
// separates, single and double quotes group. No shell runs it, so shell
// syntax is refused outright, quoted or not.
func SplitArgv(cmd string) ([]string, error) {
	if i := strings.IndexAny(cmd, "$`;|&<>\n"); i >= 0 {
		c := cmd[i : i+1]
		if c == "\n" {
			c = "\n (newline)"
		}
		return nil, fmt.Errorf("host step cmd contains the character %s; a host step is an argument vector, call a script file for shell syntax", c)
	}
	var out []string
	var cur strings.Builder
	var quote rune
	started := false
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == ' ' || r == '\t' || r == '\r':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("host step cmd has an unterminated %c quote", quote)
	}
	if started {
		out = append(out, cur.String())
	}
	return out, nil
}

// ValidatePlan checks the closed kind set, unique ids, known modes and once
// values, and that every host step's cmd is a clean argument vector.
func ValidatePlan(p Plan) error {
	if p.NoSeed && p.SeedStep() != nil {
		return fmt.Errorf("plan has no_seed and an exec once data step %q; pick one", p.SeedStep().ID)
	}
	seen := map[string]bool{}
	for i, s := range p.Steps {
		if s.ID == "" {
			return fmt.Errorf("step %d has no id", i+1)
		}
		if seen[s.ID] {
			return fmt.Errorf("step %q: duplicate id", s.ID)
		}
		seen[s.ID] = true
		if s.ID == KindSecrets || s.Kind == KindSecrets {
			return fmt.Errorf("step %q: the %q step is added by the runner to every run and cannot be declared", s.ID, KindSecrets)
		}
		// only what the trust dialog must show verbatim: host and prepare
		// steps (and every id). Exec, note and compose-up values are not
		// hashed and are quoted for display, so old files keep loading.
		vals := []string{s.ID}
		if s.Kind == KindHost || s.Kind == KindPrepare {
			vals = append(append(append(vals, s.Cmd, s.Name, s.Dir, s.Service), s.Args...), s.PortEnv...)
		}
		for _, v := range vals {
			if strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return fmt.Errorf("step %q: a value has a control character (newline, escape, tab); the trust dialog must show it as written", s.ID)
			}
		}
		switch s.Kind {
		case KindPrepare:
			if err := CheckPrepareArgs(s.Args); err != nil {
				return fmt.Errorf("step %q: %w", s.ID, err)
			}
		case KindComposeUp, KindExec, KindHost, KindNote:
		default:
			return fmt.Errorf("step %q: unknown kind %q (expected prepare, compose-up, exec, host or note)", s.ID, s.Kind)
		}
		for _, m := range s.Modes {
			if m != ModeContainer && m != ModeHybrid {
				return fmt.Errorf("step %q: unknown mode %q", s.ID, m)
			}
		}
		if s.Once != "" && (s.Kind != KindExec || (s.Once != OnceData && s.Once != OnceSchema)) {
			return fmt.Errorf("step %q: once %q is not valid here (exec steps take data or schema)", s.ID, s.Once)
		}
		if s.Port != 0 && (s.Kind != KindHost || s.Port < 1 || s.Port > 65535) {
			return fmt.Errorf("step %q: port %d is not valid here (host steps take 1..65535)", s.ID, s.Port)
		}
		if s.Kind == KindHost {
			argv, err := SplitArgv(s.Cmd)
			if err == nil && len(argv) == 0 {
				err = fmt.Errorf("host step cmd is empty")
			}
			if err != nil {
				return fmt.Errorf("step %q: %w", s.ID, err)
			}
		}
	}
	return nil
}

// CheckPrepareArgs allows in a prepare step only what recipePrepareArgs
// produces: -f/--file/--env-file/--hostname with one plain value each, and
// --reassign-ports and --random-ports. Anything else (above all --secret,
// which splitArgs lifts from anywhere in argv) would reach prepare without
// the secrets preflight seeing it. The message names the flag, never a value.
func CheckPrepareArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-f", "--file", "--env-file", "--hostname":
			if i+1 >= len(args) {
				return fmt.Errorf("prepare argument %s has no value", a)
			}
			i++
			if v := args[i]; v == "" || strings.HasPrefix(v, "-") || strings.ContainsAny(v, "$`;&|<>") {
				return fmt.Errorf("prepare argument %s needs a plain value (no leading '-', no shell characters)", a)
			}
		case "--reassign-ports", "--random-ports":
		default:
			return fmt.Errorf("prepare argument %s is not allowed in a plan; a prepare step takes only -f, --file, --env-file, --hostname, --reassign-ports and --random-ports (secrets come from the machine map: stackfork secret map)", argLabel(a, i+1))
		}
	}
	return nil
}

// argLabel names a refused argument without echoing a value: a flag by its
// name (before any "="), anything else by position.
func argLabel(a string, pos int) string {
	name, _, _ := strings.Cut(a, "=")
	if strings.HasPrefix(name, "-") && strings.Trim(name, "-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") == "" {
		return name
	}
	return fmt.Sprintf("number %d (not a flag)", pos)
}

// recipePrepareArgs mirrors cli.recipePrepareArgs: the safe prepare flags in
// a recorded "stackfork prepare ..." command, or nil.
func recipePrepareArgs(cmd string) []string {
	toks := strings.Fields(cmd)
	for i, t := range toks {
		if t != "prepare" {
			continue
		}
		var out []string
		rest := toks[i+1:]
		for j := 0; j < len(rest); j++ {
			switch f := rest[j]; f {
			case "-f", "--file", "--env-file", "--hostname":
				if j+1 >= len(rest) || strings.ContainsAny(rest[j+1], "$`;&|<>") {
					return nil
				}
				j++
				out = append(out, f, strings.Trim(rest[j], `"'`))
			case "--reassign-ports", "--random-ports":
				out = append(out, f)
			default:
				return nil
			}
		}
		return out
	}
	return nil
}

// FromRecipe converts a schema 1 recipe to the equivalent plan.
func FromRecipe(r Recipe) Plan {
	p := Plan{Modes: []string{ModeContainer}, Status: PlanRecorded}
	// seed_pending leaves the data step out (undecided: auto-detect); the plan
	// is still recorded, so a re-scan never replaces it.
	p.Steps = append(p.Steps,
		Step{ID: "prepare", Kind: KindPrepare, Args: recipePrepareArgs(r.Prepare)},
		Step{ID: "up", Kind: KindComposeUp})
	if r.Migrate != "" {
		p.Steps = append(p.Steps, Step{ID: "migrate", Kind: KindExec, Cmd: r.Migrate, Once: OnceSchema})
	}
	p.NoSeed = r.SeedOnce == "none" && !r.SeedPending
	if r.SeedOnce != "" && r.SeedOnce != "none" && !r.SeedPending {
		p.Steps = append(p.Steps, Step{ID: "seed", Kind: KindExec, Service: r.SeedService, Cmd: r.SeedOnce, Once: OnceData})
	}
	for i, l := range r.Login {
		id := "login"
		if i > 0 {
			id = fmt.Sprintf("login-%d", i+1)
		}
		p.Steps = append(p.Steps, Step{ID: id, Kind: KindNote, Text: l})
	}
	return p
}

// detail is the one-line summary Markdown shows for a step.
func (s Step) detail() string {
	var parts []string
	if len(s.Modes) > 0 {
		parts = append(parts, "["+strings.Join(s.Modes, ",")+"]")
	}
	switch s.Kind {
	case KindPrepare:
		parts = append(parts, strings.Join(s.Args, " "))
	case KindComposeUp:
		if len(s.Exclude) > 0 {
			parts = append(parts, "exclude "+strings.Join(s.Exclude, ","))
		}
	case KindExec:
		if s.Service != "" {
			parts = append(parts, s.Service+":")
		}
		parts = append(parts, s.Cmd)
		if s.Once != "" {
			parts = append(parts, "(once "+s.Once+")")
		}
	case KindHost:
		parts = append(parts, s.Cmd)
	case KindNote:
		parts = append(parts, s.Text)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
