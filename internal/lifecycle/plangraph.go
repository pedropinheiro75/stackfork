package lifecycle

import (
	"reflect"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// Graph is the startup workflow of one plan as the Project page draws it
// (docs/design/project-page/api.md): the steps a Start runs with the two
// decisions the runner really makes (Mode, New data volume), host apps that
// start together, and every node flagged in or out of the viewed mode.
type Graph struct {
	Nodes    []GraphNode `json:"nodes"`
	Edges    []GraphEdge `json:"edges"`
	Parallel [][]string  `json:"parallel"`
}

// GraphNode is a terminal (start, ready), a decision or a step.
type GraphNode struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"` // terminal, decision, step
	StepKind        string   `json:"step_kind,omitempty"`
	Where           string   `json:"where,omitempty"` // stackfork, docker, host
	Service         string   `json:"service,omitempty"`
	Title           string   `json:"title,omitempty"`
	Subtitle        string   `json:"subtitle,omitempty"`
	Command         string   `json:"command,omitempty"`
	CommandExpanded string   `json:"command_expanded"` // placeholders filled in; empty until that lands
	Once            string   `json:"once,omitempty"`
	PlanState       string   `json:"plan_state,omitempty"` // at-start, verified, trusted, draft, untrusted, derived
	SkipReason      string   `json:"skip_reason,omitempty"`
	InMode          bool     `json:"in_mode"`
	Label           string   `json:"label,omitempty"`    // decision question, or the entry target of ready
	Branches        []string `json:"branches,omitempty"` // decision
	EntryURL        string   `json:"entry_url,omitempty"`
}

// GraphEdge joins two nodes; Label names a decision branch.
type GraphEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

// GraphOpts are what PlanGraph needs beyond the plan.
type GraphOpts struct {
	// Root and Main are the checkout whose files decide the seed (the main
	// checkout on the Project page), as for PlanView.
	Root, Main string
	// K is the knowledge the plan came from; nil with no plan steps means the
	// plan is derived (nobody recorded it).
	K         *knowledge.Knowledge
	Derived   bool
	StateBase string // where the host-step trust lives
	// HasSecrets: the preflight has something to resolve; else it is shown skipped.
	HasSecrets            bool
	EntryTarget, EntryURL string
}

type gItem struct {
	steps []knowledge.Step // one step, or host apps that start together
}

type gFront struct{ id, label string }

// PlanGraph draws plan for the viewed mode. Both modes the plan supports are
// drawn from PlanRunner.Steps (the runner's own list); nodes the viewed mode
// does not run have InMode false.
func PlanGraph(plan knowledge.Plan, mode string, o GraphOpts) Graph {
	modes := []string{}
	for _, m := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
		if plan.SupportsMode(m) {
			modes = append(modes, m)
		}
	}
	if len(modes) == 0 {
		modes = []string{knowledge.ModeContainer}
	}
	if !contains(modes, mode) {
		mode = modes[0]
	}
	b := &graphBuilder{plan: plan, o: o, seen: map[string]int{}, inMode: map[string]map[string]bool{},
		edgeSet: map[GraphEdge]bool{}, decisionOf: map[string]string{}}
	trusted := false
	if o.StateBase != "" {
		trusted, _ = HashTrusted(o.StateBase, o.Main, plan)
	}
	b.trusted = trusted

	items := map[string][]gItem{}
	for _, m := range modes {
		items[m] = groupItems(PlanRunner{Root: o.Root, Main: o.Main, Plan: plan, Mode: m}.Steps())
	}
	split := -1 // index of the first item the modes disagree on
	if len(modes) == 2 {
		a, h := items[modes[0]], items[modes[1]]
		split = 0
		for split < len(a) && split < len(h) && reflect.DeepEqual(a[split], h[split]) {
			split++
		}
		if split == len(a) && split == len(h) {
			split = -1 // the same list in both modes: no decision
		}
	}

	b.node(GraphNode{ID: "start", Kind: "terminal"}, "")
	sec := GraphNode{ID: knowledge.KindSecrets, Kind: "step", StepKind: knowledge.KindSecrets, Where: "stackfork", Title: knowledge.KindSecrets,
		Subtitle: "preflight", Command: PreflightText, PlanState: "at-start"}
	if !o.HasSecrets {
		sec.SkipReason = "no secrets"
	}
	b.node(sec, "")
	b.edge("start", knowledge.KindSecrets, "")
	for _, m := range modes {
		b.walk(items[m], m, split, modes)
	}
	b.node(GraphNode{ID: "ready", Kind: "terminal", Label: o.EntryTarget, EntryURL: o.EntryURL}, "")
	for _, f := range b.ends {
		b.edge(f.id, "ready", f.label)
	}
	for i := range b.g.Nodes {
		n := &b.g.Nodes[i]
		n.InMode = len(b.inMode[n.ID]) == 0 || b.inMode[n.ID][mode]
	}
	return b.g
}

// groupItems puts host apps that follow each other into one item: they start together.
func groupItems(steps []knowledge.Step) []gItem {
	var out []gItem
	for _, s := range steps {
		if n := len(out); s.Kind == knowledge.KindHost && n > 0 && out[n-1].steps[0].Kind == knowledge.KindHost {
			out[n-1].steps = append(out[n-1].steps, s)
			continue
		}
		out = append(out, gItem{steps: []knowledge.Step{s}})
	}
	return out
}

type graphBuilder struct {
	g          Graph
	plan       knowledge.Plan
	o          GraphOpts
	trusted    bool
	seen       map[string]int
	inMode     map[string]map[string]bool
	edgeSet    map[GraphEdge]bool
	decisionOf map[string]string // compose-up id -> its New data volume decision
	ends       []gFront
	endSet     map[gFront]bool
}

// node adds n once; mode records which mode's list holds it.
func (b *graphBuilder) node(n GraphNode, mode string) {
	if _, ok := b.seen[n.ID]; !ok {
		b.seen[n.ID] = len(b.g.Nodes)
		b.g.Nodes = append(b.g.Nodes, n)
	}
	if mode != "" {
		if b.inMode[n.ID] == nil {
			b.inMode[n.ID] = map[string]bool{}
		}
		b.inMode[n.ID][mode] = true
	}
}

func (b *graphBuilder) edge(from, to, label string) {
	e := GraphEdge{from, to, label}
	if !b.edgeSet[e] {
		b.edgeSet[e] = true
		b.g.Edges = append(b.g.Edges, e)
	}
}

// walk adds one mode's items after secrets. At item index split the modes
// part: the frontier goes into the Mode decision and this mode's branch
// starts from it.
func (b *graphBuilder) walk(items []gItem, mode string, split int, modes []string) {
	cur := []gFront{{knowledge.KindSecrets, ""}}
	link := func(to, label string) {
		for _, f := range cur {
			l := f.label
			if label != "" {
				l = label
			}
			b.edge(f.id, to, l)
		}
	}
	firstUp := ""
	for i := 0; i <= len(items); i++ {
		if i == split {
			b.node(GraphNode{ID: "mode", Kind: "decision", Label: "Mode?", Branches: modes}, "")
			link("mode", "")
			cur = []gFront{{"mode", mode}}
		}
		if i == len(items) {
			break
		}
		it := items[i]
		if len(it.steps) > 1 { // host apps that start together
			ids := []string{}
			for _, s := range it.steps {
				b.node(b.stepNode(s), mode)
				link(s.ID, "start together")
				ids = append(ids, s.ID)
			}
			b.addParallel(ids)
			cur = cur[:0:0]
			for _, id := range ids {
				cur = append(cur, gFront{id, ""})
			}
			continue
		}
		s := it.steps[0]
		isSeed := s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData
		if isSeed && firstUp != "" && i > 0 && items[i-1].steps[0].ID == firstUp {
			d := b.decisionOf[firstUp]
			if d == "" {
				d = "new-volume"
				if _, taken := b.seen[d]; taken {
					d += "-" + mode
				}
				b.decisionOf[firstUp] = d
			}
			b.node(GraphNode{ID: d, Kind: "decision", Label: "New data volume?", Branches: []string{"yes", "no"}}, mode)
			link(d, "")
			b.node(b.stepNode(s), mode)
			b.edge(d, s.ID, "yes")
			cur = []gFront{{s.ID, ""}, {d, "no"}}
			continue
		}
		b.node(b.stepNode(s), mode)
		link(s.ID, "")
		cur = []gFront{{s.ID, ""}}
		if s.Kind == knowledge.KindComposeUp && firstUp == "" {
			firstUp = s.ID
		}
	}
	for _, f := range cur {
		if b.endSet == nil {
			b.endSet = map[gFront]bool{}
		}
		if !b.endSet[f] {
			b.endSet[f] = true
			b.ends = append(b.ends, f)
		}
	}
}

func (b *graphBuilder) addParallel(ids []string) {
	for _, p := range b.g.Parallel {
		if reflect.DeepEqual(p, ids) {
			return
		}
	}
	b.g.Parallel = append(b.g.Parallel, ids)
}

// stepNode is the card of one step: where it runs, how far it is trusted and
// whether a Start would skip it because the setup is only a draft.
func (b *graphBuilder) stepNode(s knowledge.Step) GraphNode {
	n := GraphNode{ID: s.ID, Kind: "step", StepKind: s.Kind, Title: s.ID, Once: s.Once, Command: StepText(s), Service: s.Service}
	switch s.Kind {
	case knowledge.KindPrepare:
		n.Where, n.Subtitle = "stackfork", "prepare"
	case knowledge.KindNote:
		n.Where, n.Subtitle = "stackfork", "note"
	case knowledge.KindComposeUp:
		n.Where, n.Subtitle = "docker", "compose up"
	case knowledge.KindHost:
		n.Where, n.Subtitle = "host", "host app"
	default: // exec
		n.Where, n.Subtitle = "docker", "exec"
		if s.IsHostExec() {
			n.Where = "host"
		} else if n.Service == "" {
			n.Service = b.plan.AppService
		}
		if s.Once != "" {
			n.Subtitle += " · once " + s.Once
		}
	}
	k := b.o.K
	switch {
	case b.o.Derived || k == nil:
		n.PlanState = StateDerived
	case k.Status == knowledge.StatusVerified:
		n.PlanState = knowledge.StatusVerified
	default:
		n.PlanState = knowledge.StatusDraft
	}
	if n.PlanState != StateDerived && (s.Kind == knowledge.KindHost || s.Kind == knowledge.KindPrepare || s.IsHostExec()) {
		n.PlanState = "untrusted"
		if b.trusted {
			n.PlanState = "trusted"
		}
	}
	if s.Kind == knowledge.KindExec && isDraft(k) && b.plan.Status != knowledge.PlanRecorded {
		switch s.Once {
		case knowledge.OnceData:
			n.SkipReason, n.PlanState = DraftSkip("seed", s.ID, s.Once), knowledge.StatusDraft
		case knowledge.OnceSchema:
			n.SkipReason, n.PlanState = DraftSkip("migrate", s.ID, s.Once), knowledge.StatusDraft
		}
	}
	return n
}
