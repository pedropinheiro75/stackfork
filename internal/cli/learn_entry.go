package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const (
	entryUsage = "learn record entry --service S --port N [--path P] | learn record entry --none | learn record entry --remove"
	linkUsage  = "learn record link NAME --path P | learn record link NAME --service S --port N [--path P] | learn record link --remove NAME"
)

// stackView is what `learn record entry|link` checks a target against: the
// prepared instance's Compose model, else the scanned services (D4).
type stackView struct {
	targets map[string][]int
	known   bool
	note    string
}

// learnStack reads the checkout's prepared instance, when it has one.
func learnStack(root, newStateBase, legacyStateBase string, k *knowledge.Knowledge) stackView {
	if model := preparedModel(root, newStateBase, legacyStateBase); model != nil {
		return stackView{targets: compose.ServiceTargets(model), known: true}
	}
	v := stackView{targets: map[string][]int{}}
	for _, s := range k.Facts.Services {
		v.targets[s.Name] = nil
		for _, p := range s.Ports {
			target := strings.SplitN(p[strings.LastIndex(p, ":")+1:], "/", 2)[0]
			if n, err := strconv.Atoi(target); err == nil {
				v.targets[s.Name] = append(v.targets[s.Name], n)
			}
		}
	}
	v.known = len(v.targets) > 0
	if v.known {
		v.note = "checked against the scanned Compose services; the adapter may add services the scan does not see"
	} else {
		v.note = "no prepared instance and no scanned services: the target was not checked"
	}
	return v
}

// preparedModel returns the Compose model of root's prepared instance, or
// nil when root is not a git checkout or has no prepared stack.
func preparedModel(root, newStateBase, legacyStateBase string) interface{} {
	toplevel, err := gitToplevel(root)
	if err != nil {
		return nil
	}
	top, err := identity.ResolvePath(toplevel)
	if err != nil {
		return nil
	}
	branch, err := currentBranch(top)
	if err != nil {
		return nil
	}
	_, instanceDir, err := checkoutInstance(top, branch, newStateBase, legacyStateBase)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil
	}
	model, err := compose.DecodeJSON(data)
	if err != nil {
		return nil
	}
	return model
}

func (v stackView) check(what, service string, port int) error {
	if !v.known {
		return nil
	}
	for _, p := range v.targets[service] {
		if p == port {
			return nil
		}
	}
	names := make([]string, 0, len(v.targets))
	for n := range v.targets {
		names = append(names, n)
	}
	sort.Strings(names)
	var have []string
	for _, n := range names {
		ports := "no ports"
		if len(v.targets[n]) > 0 {
			ps := append([]int(nil), v.targets[n]...)
			sort.Ints(ps)
			strs := make([]string, len(ps))
			for i, p := range ps {
				strs[i] = strconv.Itoa(p)
			}
			ports = strings.Join(strs, ", ")
		}
		have = append(have, fmt.Sprintf("%s (%s)", n, ports))
	}
	return fmt.Errorf("%s %s:%d is not in the stack; the stack has: %s", what, service, port, strings.Join(have, ", "))
}

// parseNamed parses flags with NAME positionals in any order (as
// recordSecret does) and returns the positionals.
func parseNamed(fs *flag.FlagSet, rest []string) ([]string, error) {
	var names []string
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, parseFailureErr(fs, err)
		}
		if fs.NArg() == 0 {
			return names, nil
		}
		names = append(names, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

// recordEntry is `learn record entry`.
func recordEntry(k *knowledge.Knowledge, rest []string, stack func() stackView) (note string, err error) {
	fs := newFlagSet(io.Discard, "learn record entry")
	service := fs.String("service", "", "")
	port := fs.Int("port", 0, "")
	path := fs.String("path", "", "")
	none := fs.Bool("none", false, "")
	remove := fs.Bool("remove", false, "")
	extra, err := parseNamed(fs, rest)
	if err != nil {
		return "", err
	}
	usage := errors.New("usage: " + entryUsage)
	if len(extra) > 0 {
		return "", usage
	}
	switch {
	case *remove:
		if *none || *service != "" || *port != 0 || *path != "" {
			return "", usage
		}
		if k.Entry == nil {
			return "", errors.New("no entry is recorded")
		}
		k.Entry = nil
		return orphanLinksNote(k), nil
	case *none:
		if *service != "" || *port != 0 || *path != "" {
			return "", usage
		}
		k.Entry = &knowledge.Entry{None: true}
		return orphanLinksNote(k), knowledge.ValidateEntryLinks(k.Entry, k.Links)
	}
	if *service == "" || *port == 0 {
		return "", usage
	}
	e := &knowledge.Entry{Service: *service, Port: *port, Path: *path}
	if err := knowledge.ValidateEntryLinks(e, k.Links); err != nil {
		return "", err
	}
	v := stack()
	if err := v.check("the entry", e.Service, e.Port); err != nil {
		return "", err
	}
	k.Entry = e
	return v.note, nil
}

// orphanLinksNote names the links that follow the entry (no service of
// their own), which have no target once the entry is gone.
func orphanLinksNote(k *knowledge.Knowledge) string {
	var names []string
	for _, l := range k.Links {
		if l.Service == "" {
			names = append(names, l.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "these links follow the entry and now have no target: " + strings.Join(names, ", ")
}

// recordLink is `learn record link`.
func recordLink(k *knowledge.Knowledge, rest []string, stack func() stackView) (note string, err error) {
	fs := newFlagSet(io.Discard, "learn record link")
	service := fs.String("service", "", "")
	port := fs.Int("port", 0, "")
	path := fs.String("path", "", "")
	remove := fs.String("remove", "", "")
	names, err := parseNamed(fs, rest)
	if err != nil {
		return "", err
	}
	usage := errors.New("usage: " + linkUsage)
	if *remove != "" {
		if len(names) > 0 || *service != "" || *port != 0 || *path != "" {
			return "", usage
		}
		if !k.RemoveLink(*remove) {
			return "", errors.New("no link has that name")
		}
		return "", nil
	}
	if len(names) != 1 {
		return "", usage
	}
	l := knowledge.Link{Name: names[0], Service: *service, Port: *port, Path: *path}
	if l.Service == "" && l.Port == 0 && l.Path == "" || l.Service != "" && l.Port == 0 {
		return "", usage
	}
	// A path (or a bare port) hangs off the entry, so an entry must exist.
	if l.Service == "" && (k.Entry == nil || k.Entry.None) {
		return "", errors.New("this link is on the entry, but no entry is recorded; record it first with `learn record entry` (or give --service and --port)")
	}
	candidate := append([]knowledge.Link(nil), k.Links...)
	probe := &knowledge.Knowledge{Links: candidate}
	probe.UpsertLink(l)
	if err := knowledge.ValidateEntryLinks(k.Entry, probe.Links); err != nil {
		return "", err
	}
	target := l.Service
	if target == "" && l.Port != 0 {
		target = k.Entry.Service
	}
	if l.Port != 0 {
		v := stack()
		if err := v.check("link "+l.Name+" targets", target, l.Port); err != nil {
			return "", err
		}
		note = v.note
	}
	k.UpsertLink(l)
	return note, nil
}
