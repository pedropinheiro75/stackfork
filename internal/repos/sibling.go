package repos

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// SiblingRecipe is a prepare recipe borrowed from another worktree of the
// same repo (add-repo-browser design.md D6a): the compose files and env file
// its prepared instance used, as paths relative to its root, plus the secret
// references (never values).
type SiblingRecipe struct {
	Source  string            // the sibling checkout
	Project string            // its instance
	Files   []string          // compose files, relative to the checkout root
	EnvFile string            // relative; "" when the instance had none
	Secrets map[string]string // name -> reference
	// SeedOnce/SeedService: the sibling's recorded seed recipe, if any.
	SeedOnce, SeedService string
	// untracked lists the Files (and EnvFile) git does not track: Apply
	// copies their folders from the sibling.
	untracked []string
	// skipKnowledge: Apply leaves knowledge.json where it is (the main
	// recipe: a worktree reads the main checkout's file).
	skipKnowledge bool
}

// SiblingReport lists exactly what Apply did. It names files and env keys,
// never values.
type SiblingReport struct {
	Copied    []string
	Rewritten []string // env keys whose value was changed to the new checkout
	Skipped   []string
}

const maxAdapterFile = 1 << 20

func sameDir(a, b string) bool {
	ra, e1 := filepath.EvalSymlinks(a)
	rb, e2 := filepath.EvalSymlinks(b)
	return e1 == nil && e2 == nil && ra == rb
}

// DefaultBranchOf names the ref tracked compose files must exist on for a
// brand-new worktree: origin/HEAD, else main or master, else HEAD.
func DefaultBranchOf(main string) string {
	if out, err := gitRun(main, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(out, "origin/"); b != "" && refExists(main, "refs/heads/"+b) {
			return b
		}
	}
	for _, n := range []string{"main", "master"} {
		if refExists(main, "refs/heads/"+n) {
			return n
		}
	}
	return "HEAD"
}

// FindSiblingRecipe looks for a prepared instance whose root is another
// checkout of the repo whose main checkout is main, most recently used
// first, and returns the first one whose recipe can be rebuilt for a new
// worktree. Tracked compose files must exist in target (or, when target is
// "", on ref: the readiness check); untracked ones are copied from the
// sibling by Apply. It returns (nil, nil) when there is no candidate and an
// error saying why when candidates exist but none is usable.
func FindSiblingRecipe(main, target, ref string, insts []inventory.Instance) (*SiblingRecipe, error) {
	var cands []inventory.Instance
	for _, i := range insts {
		if i.Root == "" || len(i.Files) == 0 || (target != "" && sameDir(i.Root, target)) {
			continue
		}
		if fi, err := os.Stat(i.Root); err != nil || !fi.IsDir() {
			continue
		}
		if m := MainRepoOf(i.Root); m == "" || !sameDir(m, main) {
			continue
		}
		cands = append(cands, i)
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].Used.After(cands[b].Used) })
	var first error
	for _, c := range cands {
		r, err := recipeFrom(c, main, target, ref)
		if err == nil {
			return r, nil
		}
		if first == nil {
			first = err
		}
	}
	return nil, first
}

func recipeFrom(c inventory.Instance, main, target, ref string) (*SiblingRecipe, error) {
	r := &SiblingRecipe{Source: c.Root, Project: c.Project, Secrets: c.Secrets, SeedOnce: c.SeedOnce, SeedService: c.SeedService}
	where := "the new checkout"
	if target == "" {
		where = ref
	}
	rel := func(p string) (string, error) {
		x, err := filepath.Rel(c.Root, p)
		if err != nil || x == ".." || strings.HasPrefix(x, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%s (used by %s) is outside %s, so it cannot be reused", p, c.Project, c.Root)
		}
		return filepath.ToSlash(x), nil
	}
	tracked := func(x string) (bool, error) {
		if _, err := gitRun(c.Root, "ls-files", "--error-unmatch", "--", x); err != nil {
			return false, nil
		}
		var ok bool
		if target != "" {
			_, err := os.Stat(filepath.Join(target, filepath.FromSlash(x)))
			ok = err == nil
		} else {
			_, err := gitRun(main, "cat-file", "-e", ref+":"+x)
			ok = err == nil
		}
		if !ok {
			return true, fmt.Errorf("%s (used by %s) is tracked in %s but is not in %s", x, c.Project, filepath.Base(c.Root), where)
		}
		return true, nil
	}
	for _, f := range c.Files {
		x, err := rel(f)
		if err != nil {
			return nil, err
		}
		t, err := tracked(x)
		if err != nil {
			return nil, err
		}
		if !t {
			if _, err := os.Stat(f); err != nil {
				return nil, fmt.Errorf("%s (used by %s) no longer exists", f, c.Project)
			}
			if why := Denied(x, f); why != "" {
				return nil, fmt.Errorf("%s (used by %s) is a %s and is never copied", x, c.Project, why)
			}
			r.untracked = append(r.untracked, x)
		}
		r.Files = append(r.Files, x)
	}
	if c.EnvFile != "" {
		x, err := rel(c.EnvFile)
		if err != nil {
			return nil, err
		}
		t, err := tracked(x)
		if err != nil {
			return nil, err
		}
		if !t {
			d := filepath.Base(filepath.Dir(x))
			if filepath.Base(x) != "runtime.env" || (d != ".agent-dev" && d != ".stackfork") {
				return nil, fmt.Errorf("env file %s (used by %s) is not a stackfork runtime env, so it is never copied; create it in the new checkout", x, c.Project)
			}
			if _, err := os.Stat(c.EnvFile); err != nil {
				return nil, fmt.Errorf("env file %s (used by %s) no longer exists", c.EnvFile, c.Project)
			}
			r.untracked = append(r.untracked, x)
		}
		r.EnvFile = x
	}
	return r, nil
}

// Apply copies the untracked adapter files into target (never over an
// existing file, never anything the credential denylist matches, no logs,
// no folders) and writes the runtime env with checkout-specific values
// rewritten, mode 0600.
func (r *SiblingRecipe) Apply(target string) (SiblingReport, error) {
	var rep SiblingReport
	rep.Copied = r.copyRegistryConfig(target)
	dirs := map[string]bool{}
	for _, x := range r.untracked {
		dirs[filepath.Dir(x)] = true
	}
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		entries, err := os.ReadDir(filepath.Join(r.Source, d))
		if err != nil {
			return rep, err
		}
		for _, e := range entries {
			x := filepath.ToSlash(filepath.Join(d, e.Name()))
			dst := filepath.Join(target, filepath.FromSlash(x))
			if !e.Type().IsRegular() || x == r.EnvFile || strings.HasSuffix(e.Name(), ".log") || (r.skipKnowledge && e.Name() == "knowledge.json") {
				continue
			}
			if _, err := os.Lstat(dst); err == nil {
				continue // already there (tracked, copied earlier or edited)
			}
			if fi, err := e.Info(); err == nil && fi.Size() > maxAdapterFile {
				rep.Skipped = append(rep.Skipped, x+" (larger than 1 MiB)")
				continue
			}
			c, s := copyTree(filepath.Join(r.Source, filepath.FromSlash(x)), dst, x)
			rep.Copied, rep.Skipped = append(rep.Copied, c...), append(rep.Skipped, s...)
		}
	}
	if r.EnvFile == "" || !r.isUntracked(r.EnvFile) {
		return rep, nil
	}
	dst := filepath.Join(target, filepath.FromSlash(r.EnvFile))
	if _, err := os.Lstat(dst); err == nil {
		return rep, nil
	}
	b, err := os.ReadFile(filepath.Join(r.Source, filepath.FromSlash(r.EnvFile)))
	if err != nil {
		return rep, err
	}
	froms := []string{r.Source}
	if alt := strings.TrimPrefix(r.Source, "/private"); alt != r.Source {
		froms = append(froms, alt) // macOS spells /var, /tmp without the /private prefix
	}
	out, keys := rewriteEnv(b, froms, target, dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return rep, err
	}
	if err := os.WriteFile(dst, out, 0o600); err != nil {
		return rep, err
	}
	rep.Copied = append(rep.Copied, r.EnvFile)
	rep.Rewritten = keys
	return rep, nil
}

func (r *SiblingRecipe) isUntracked(x string) bool {
	for _, u := range r.untracked {
		if u == x {
			return true
		}
	}
	return false
}

// rewriteEnv points checkout-specific values of a KEY=VALUE env file at the
// new checkout: DEV_CHECKOUT, DEV_RUNTIME_ENV, and any value that is the old
// root or a path under it. It returns the keys it changed.
func rewriteEnv(src []byte, froms []string, to, toEnv string) ([]byte, []string) {
	var keys []string
	lines := strings.Split(string(src), "\n")
	for n, line := range lines {
		i := strings.IndexByte(line, '=')
		if i < 0 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key := strings.TrimPrefix(strings.TrimSpace(line[:i]), "export ")
		val := line[i+1:]
		bare := strings.Trim(strings.TrimSpace(val), `"'`)
		var nv string
		switch {
		case key == "DEV_CHECKOUT":
			nv = to
		case key == "DEV_RUNTIME_ENV":
			nv = toEnv
		default:
			for _, from := range froms {
				if bare == from || strings.HasPrefix(bare, from+"/") {
					nv = to + strings.TrimPrefix(bare, from)
				}
			}
			if nv == "" {
				continue
			}
		}
		if q := strings.TrimSpace(val); q != "" && (q[0] == '"' || q[0] == '\'') {
			nv = q[:1] + nv + q[:1]
		}
		if nv != val {
			lines[n] = line[:i+1] + nv
			keys = append(keys, key)
		}
	}
	return []byte(strings.Join(lines, "\n")), keys
}

// registryFiles are the repo-root package-manager configs that point an
// install at a private registry.
var registryFiles = []string{".npmrc", ".yarnrc.yml", ".yarnrc", ".pnpmrc", "bunfig.toml"}

var envRef = regexp.MustCompile(`^\$(\{[A-Za-z_][A-Za-z0-9_]*\}|[A-Za-z_][A-Za-z0-9_]*)$`)

// refOnlyRegistry reports whether every auth-looking value in a registry
// config is an environment reference (${VAR}), never a literal, and no URL
// carries credentials.
func refOnlyRegistry(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if i := strings.Index(line, "://"); i >= 0 {
			if rest := line[i+3:]; strings.Contains(strings.SplitN(rest, "/", 2)[0], "@") {
				return false
			}
		}
		low := strings.ToLower(line)
		if !strings.ContainsAny(low, "=:") && !strings.Contains(low, " ") {
			continue
		}
		for _, kw := range []string{"auth", "token", "password", "secret", "_pass"} {
			if !strings.Contains(low, kw) {
				continue
			}
			if !authValuesAreRefs(line) {
				return false
			}
			break
		}
	}
	return true
}

// authValuesAreRefs checks each value on an auth-looking line (npmrc
// "k=v", yaml "k: v", bunfig inline "{ token = "v", url = ... }").
func authValuesAreRefs(line string) bool {
	line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), " }"))
	for _, seg := range strings.Split(line, ",") {
		i := strings.LastIndexAny(seg, "=")
		if i < 0 {
			if i = strings.Index(seg, ": "); i < 0 {
				if i = strings.IndexByte(seg, ' '); i < 0 {
					return false
				}
			}
		}
		key := strings.ToLower(seg[:i])
		val := strings.Trim(strings.TrimSpace(seg[i+1:]), "\"'")
		sensitive := false
		for _, kw := range []string{"auth", "token", "password", "secret", "_pass"} {
			sensitive = sensitive || strings.Contains(key, kw)
		}
		if sensitive && !envRef.MatchString(val) {
			return false
		}
	}
	return true
}

// copyRegistryConfig copies untracked root-level registry configs whose auth
// values are all env references, never over an existing file.
func (r *SiblingRecipe) copyRegistryConfig(target string) []string {
	var copied []string
	for _, f := range registryFiles {
		src, dst := filepath.Join(r.Source, f), filepath.Join(target, f)
		if _, err := gitRun(r.Source, "ls-files", "--error-unmatch", "--", f); err == nil {
			continue // tracked: the worktree has its own
		}
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxAdapterFile {
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil || !refOnlyRegistry(b) {
			continue
		}
		if os.WriteFile(dst, b, fi.Mode().Perm()) == nil {
			copied = append(copied, f)
		}
	}
	return copied
}

// SiblingSecrets returns the secret references (never values) of the most
// recently used instance of another checkout of the same repo that has
// any, so a checkout prepared from knowledge or an adapter gets the same
// registry token the other worktrees run with.
func SiblingSecrets(main, target string, insts []inventory.Instance) map[string]string {
	var best *inventory.Instance
	for i := range insts {
		c := &insts[i]
		if len(c.Secrets) == 0 || c.Root == "" || sameDir(c.Root, target) {
			continue
		}
		if m := MainRepoOf(c.Root); m == "" || !sameDir(m, main) {
			continue
		}
		if best == nil || c.Used.After(best.Used) {
			best = c
		}
	}
	if best == nil {
		return nil
	}
	return best.Secrets
}

// AdapterRecipeFromMain builds the recipe that copies, from the main
// checkout into the linked worktree root, the untracked files that prepare
// args (-f, --file, --env-file) name and root lacks. It returns (nil, nil)
// when nothing is missing, and an error naming the file when a missing file
// is tracked in main (a real missing file) or is never copied. Apply does
// the copying, so the denylist, the registry rules and the runtime env
// rewrite are the sibling path's.
func AdapterRecipeFromMain(main, root string, args []string) (*SiblingRecipe, error) {
	if main == "" || sameDir(main, root) {
		return nil, nil
	}
	r := &SiblingRecipe{Source: main}
	// missing returns the slash path of a named file that root lacks and main has.
	missing := func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		x, err := filepath.Rel(root, p)
		if err != nil || x == ".." || strings.HasPrefix(x, ".."+string(filepath.Separator)) {
			return "", nil
		}
		if _, err := os.Stat(p); err == nil {
			return "", nil
		}
		x = filepath.ToSlash(x)
		if _, err := os.Stat(filepath.Join(main, filepath.FromSlash(x))); err != nil {
			return "", nil // prepare reports a file that is nowhere
		}
		if _, err := gitRun(main, "ls-files", "--error-unmatch", "--", x); err == nil {
			return "", fmt.Errorf("%s is tracked in the main checkout but missing in %s; restore it there (it is not an adapter file, so it is not copied)", x, root)
		}
		return x, nil
	}
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-f", "--file":
			x, err := missing(args[i+1])
			if err != nil {
				return nil, err
			}
			if x == "" {
				continue
			}
			if why := Denied(x, filepath.Join(main, filepath.FromSlash(x))); why != "" {
				return nil, fmt.Errorf("%s is a %s and is never copied; create it in %s", x, why, root)
			}
			r.Files, r.untracked = append(r.Files, x), append(r.untracked, x)
		case "--env-file":
			x, err := missing(args[i+1])
			if err != nil {
				return nil, err
			}
			if x == "" {
				continue
			}
			d := filepath.Base(filepath.Dir(x))
			if filepath.Base(x) != "runtime.env" || (d != ".agent-dev" && d != ".stackfork") {
				return nil, fmt.Errorf("env file %s is not a stackfork runtime env, so it is never copied; create it in %s", x, root)
			}
			r.EnvFile, r.untracked = x, append(r.untracked, x)
		}
	}
	if len(r.untracked) == 0 {
		return nil, nil
	}
	return r, nil
}
