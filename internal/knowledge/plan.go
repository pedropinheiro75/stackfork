package knowledge

import (
	"fmt"
	"strings"
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

	Text string `json:"text,omitempty"` // note
}

// Plan is the ordered startup sequence. Steps run in list order.
type Plan struct {
	Modes      []string `json:"modes,omitempty"`
	AppService string   `json:"app_service,omitempty"`
	Status     string   `json:"status,omitempty"`
	Steps      []Step   `json:"steps,omitempty"`
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
	seen := map[string]bool{}
	for i, s := range p.Steps {
		if s.ID == "" {
			return fmt.Errorf("step %d has no id", i+1)
		}
		if seen[s.ID] {
			return fmt.Errorf("step %q: duplicate id", s.ID)
		}
		seen[s.ID] = true
		switch s.Kind {
		case KindPrepare, KindComposeUp, KindExec, KindHost, KindNote:
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
	if r.SeedPending {
		p.Status = PlanProposed
	}
	p.Steps = append(p.Steps,
		Step{ID: "prepare", Kind: KindPrepare, Args: recipePrepareArgs(r.Prepare)},
		Step{ID: "up", Kind: KindComposeUp})
	if r.Migrate != "" {
		p.Steps = append(p.Steps, Step{ID: "migrate", Kind: KindExec, Cmd: r.Migrate, Once: OnceSchema})
	}
	if r.SeedOnce != "" && r.SeedOnce != "none" {
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

// ToRecipe is the transitional inverse of FromRecipe so today's readers keep
// working until the recipe is removed.
func ToRecipe(p Plan) Recipe {
	var r Recipe
	for _, s := range p.Steps {
		switch {
		case s.Kind == KindPrepare && r.Prepare == "":
			r.Prepare = strings.Join(append([]string{"stackfork", "prepare"}, s.Args...), " ")
		case s.Kind == KindComposeUp && r.Up == "":
			r.Up = "stackfork up"
		case s.Kind == KindNote:
			r.Login = append(r.Login, s.Text)
		}
	}
	if m := p.MigrateStep(); m != nil {
		r.Migrate = m.Cmd
	}
	if s := p.SeedStep(); s != nil {
		r.SeedOnce, r.SeedService = s.Cmd, s.Service
		r.SeedPending = p.Status == PlanProposed
	}
	return r
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
