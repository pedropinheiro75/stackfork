package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SecretValue is one resolved secret and the reference it came from.
type SecretValue struct {
	Ref   string
	Value string
}

// SecretValues are the values a run resolved, by the secret's manifest name.
// They live in memory for the run (D8) and are never written anywhere. The
// reference travels with the value: a different reference under the same
// name is a different secret (review I1, I4).
type SecretValues map[string]SecretValue

// Redactions turns the values into what the run redactor takes.
func (v SecretValues) Redactions() []redact.Secret {
	names := make([]string, 0, len(v))
	for n := range v {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]redact.Secret, 0, len(names))
	for _, n := range names {
		out = append(out, redact.Secret{Name: n, Value: v[n].Value})
	}
	return out
}

// Preflight is the `secrets` step every run starts with (D8): it resolves
// every effective reference, checks that every declared name has one, and
// fails before any Docker call, listing every problem with its fix.
type Preflight struct {
	Run         *runs.Run // nil: nothing is recorded, the lines go to Out
	InstanceDir string    // the dashboard remembers a run's values for this instance (D11)
	Out         io.Writer // used when Run is nil
	Main        string    // the repository the fix commands name
	Refs        map[string]string
	Declared    []knowledge.SecretDecl
	Used        map[string]string // name -> Compose file that uses it (ComposeSecrets)
	Folders     map[string]string // name -> folder, for a reference a folder supplies
	Recorded    map[string]bool   // names whose reference is the instance's recorded one (--secret at prepare)
	Unprepared  []string          // mapped names the manifest does not record (bare up)
	Dashboard   bool              // the dashboard does not see the shell environment
}

// PreflightText is what the row says.
const PreflightText = "resolve secrets"

// errPreflight is the error of a failed step.
func preflightError(failed []string) error {
	return fmt.Errorf("step %s failed: %s; the step log lists the fix for each", knowledge.KindSecrets, strings.Join(failed, ", "))
}

// Do runs the step and returns the resolved values. With nothing to resolve
// the step is skipped with reason "no secrets".
func (p Preflight) Do() (SecretValues, error) {
	declared := map[string]string{}
	for _, d := range p.Declared {
		declared[d.Name] = d.Hint
	}
	names := map[string]bool{}
	for n := range p.Refs {
		names[n] = true
	}
	for n := range declared {
		names[n] = true
	}
	for _, n := range p.Unprepared {
		names[n] = true
	}
	for n := range p.Used {
		names[n] = true
	}
	var step *runs.Step
	if p.Run != nil {
		step = p.Run.Step(knowledge.KindSecrets, knowledge.KindSecrets, PreflightText)
	}
	if len(names) == 0 {
		if step != nil {
			step.Skip("no secrets")
		}
		return SecretValues{}, nil // non-nil: a run with no secrets, Execute resolves nothing late
	}
	w := p.Out
	if step != nil {
		w = step.Start()
	}
	if w == nil {
		w = io.Discard
	}
	order := make([]string, 0, len(names))
	for n := range names {
		order = append(order, n)
	}
	sort.Strings(order)
	unprepared := map[string]bool{}
	for _, n := range p.Unprepared {
		unprepared[n] = true
	}

	values := SecretValues{}
	var failed []string
	for _, name := range order {
		ref, hasRef := p.Refs[name]
		switch {
		case unprepared[name]:
			fmt.Fprintf(w, "%s: %v\n", name, UnpreparedError(name))
			failed = append(failed, UnpreparedError(name).Error())
		case !hasRef && p.Used[name] != "":
			fmt.Fprintf(w, "%s is used by %s but has no reference; run: stackfork secret map %s %s=npmrc:$HOME/.npmrc#npm.pkg.github.com (or keychain:%s, op://VAULT/ITEM/FIELD)\n", name, p.Used[name], shellQuote(p.Main), name, itemFor(name))
			failed = append(failed, name+" has no reference")
		case !hasRef:
			label := name
			if h := declared[name]; h != "" {
				label += " (" + h + ")"
			}
			fmt.Fprintf(w, "%s has no reference on this machine; run: stackfork secret map %s %s=keychain:%s\n", label, shellQuote(p.Main), name, itemFor(name))
			failed = append(failed, name+" has no reference")
		default:
			value, st, err := compose.CheckSecret(ref)
			if st == secretstore.Resolves {
				values[name] = SecretValue{Ref: ref, Value: value}
				fmt.Fprintf(w, "%s (%s): %s%s\n", name, ref, st, p.fromFolder(name))
				continue
			}
			fmt.Fprintf(w, "%s (%s): %s%s; %s\n", name, ref, st, p.fromFolder(name), p.fix(name, ref, st, err))
			failed = append(failed, fmt.Sprintf("%s (%s) %s", name, ref, st))
		}
	}
	if len(failed) == 0 {
		if step != nil {
			step.End(nil, 0)
		}
		if p.Dashboard && p.InstanceDir != "" {
			CacheSecrets(p.InstanceDir, values)
		}
		return values, nil
	}
	err := preflightError(failed)
	if step != nil {
		step.End(errors.New(err.Error()), 0)
	}
	return nil, err
}

// fix is the sentence after the status: the exact command, or what to do.
func (p Preflight) fix(name, ref string, st secretstore.Status, err error) string {
	item := itemFor(name)
	repo := shellQuote(p.Main)
	switch st {
	case secretstore.Locked:
		store := "the keychain"
		if strings.HasPrefix(ref, "op://") {
			store = "1Password"
		}
		return "unlock " + store + " and start again"
	case secretstore.Missing:
		switch {
		case strings.HasPrefix(ref, "keychain:"):
			return "run: stackfork secret set " + strings.TrimPrefix(ref, "keychain:")
		case strings.HasPrefix(ref, "op://"):
			return "check the 1Password reference; run: stackfork secret map " + repo + " " + name + "=op://VAULT/ITEM/FIELD"
		case strings.HasPrefix(ref, "env:"):
			v := strings.TrimPrefix(ref, "env:")
			lead := "set " + v + " in your shell, or"
			if p.Dashboard {
				lead = "the dashboard does not see your shell environment, so env: references fail when you start from the page;"
			}
			return fmt.Sprintf("%s keep it in the keychain: printenv %s | stackfork secret set %s; stackfork secret map %s %s=keychain:%s", lead, v, item, repo, name, item)
		}
	}
	out := "run: stackfork secret check " + repo
	if err != nil {
		out = firstLine(err.Error()) + "; " + out
	}
	if p.Recorded[name] { // prepare keeps a --secret reference until it is replaced or forgotten
		out += "; or give prepare a new --secret " + name + "=REF, or run: stackfork secret forget " + name
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// itemFor is a keychain item name suggested for a secret name.
func itemFor(name string) string {
	item := strings.Trim(strings.ToLower(strings.ReplaceAll(name, "_", "-")), "-")
	if !secretstore.ValidItem(item) {
		return "ITEM"
	}
	return item
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DeclaredSecrets are the names knowledge declares for a checkout.
func DeclaredSecrets(root, main string) []knowledge.SecretDecl {
	if k, _, _ := knowledge.LoadFor(root, main); k != nil {
		return k.Secrets
	}
	return nil
}

// InstancePreflight is the preflight of an existing instance (up, seed): the
// manifest's references overridden by the machine map (D7).
func InstancePreflight(rec *runs.Run, out io.Writer, root, instanceDir string, dashboard bool) (SecretValues, error) {
	info, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if info == nil {
		info = &state.Manifest{}
	}
	mapped, err := MappedSecrets(root)
	if err != nil {
		return nil, err
	}
	main := identity.MainRepoOf(root)
	if main == "" {
		main = root
	}
	refs, source, unprepared := InstanceSecrets(info, mapped)
	_, from, _ := FolderSecrets(root, info.ResolvedSecrets())
	folders := map[string]string{}
	recorded := map[string]bool{}
	for n, src := range source {
		if src == SourceFolder {
			folders[n] = from[n]
		}
		recorded[n] = src == SourceManifest
	}
	// The names the recorded Compose files use count like declared ones, so a
	// bare up fails here and not later in Docker. Best effort: files that are
	// gone or Compose that cannot render give none, as before.
	envFile := ""
	if info.EnvFile != nil {
		envFile = *info.EnvFile
	}
	return Preflight{Run: rec, Out: out, InstanceDir: instanceDir, Main: main, Refs: refs, Folders: folders, Recorded: recorded, Declared: DeclaredSecrets(root, main),
		Used: ComposeSecrets(root, info.Files, envFile), Unprepared: unprepared, Dashboard: dashboard}.Do()
}

// fromFolder is " (from folder X)" for a reference a folder mapping supplies.
func (p Preflight) fromFolder(name string) string {
	if f := p.Folders[name]; f != "" {
		return " (from folder " + f + ")"
	}
	return ""
}
