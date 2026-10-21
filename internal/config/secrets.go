package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// validateSecrets checks every map entry (D5). An error names the
// repository and the secret and says why, but never prints the entry: text
// that is not a reference may be a value pasted by mistake.
func (c *Config) validateSecrets() error {
	repos := make([]string, 0, len(c.Secrets))
	for r := range c.Secrets {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		names := make([]string, 0, len(c.Secrets[repo]))
		for n := range c.Secrets[repo] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			if why := compose.SecretNameProblem(name); why != "" {
				return fmt.Errorf("secrets for %s: %s (run `stackfork secret unmap` to remove it)", repo, why)
			}
			if err := compose.ValidateSecretRef(c.Secrets[repo][name]); err != nil {
				return fmt.Errorf("secrets for %s: %s: %w", repo, name, err)
			}
		}
	}
	return nil
}

// SecretsFor returns a copy of the references mapped for a repository.
func (c *Config) SecretsFor(repo string) map[string]string {
	out := map[string]string{}
	key := repo
	if p, err := identity.ResolvePath(repo); err == nil {
		key = p
	}
	for n, r := range c.Secrets[key] {
		out[n] = r
	}
	return out
}

// InheritedSecretsFor returns the references mapped on the folders above a
// repository (never its own entry), and the folder each comes from. A folder
// matches by path segments, and the nearest one wins per name (R2).
func (c *Config) InheritedSecretsFor(repo string) (refs, from map[string]string) {
	refs, from = map[string]string{}, map[string]string{}
	key := repo
	if p, err := identity.ResolvePath(repo); err == nil {
		key = p
	}
	var dirs []string
	for k := range c.Secrets {
		if strings.HasPrefix(key, strings.TrimRight(k, string(filepath.Separator))+string(filepath.Separator)) {
			dirs = append(dirs, k)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) < len(dirs[j]) }) // the nearest is last, so it wins
	for _, d := range dirs {
		for n, r := range c.Secrets[d] {
			refs[n], from[n] = r, d
		}
	}
	return refs, from
}

// checkMapTarget refuses a path that is neither a repository root nor a
// folder above repositories (R5); the error says why.
func checkMapTarget(p string) error {
	if p == filepath.VolumeName(p)+string(filepath.Separator) {
		return fmt.Errorf("%s is the filesystem root; map a narrower folder", p)
	}
	if h, err := os.UserHomeDir(); err == nil {
		if rh, err := identity.ResolvePath(h); err == nil && rh == p {
			return fmt.Errorf("%s is your home directory; map a narrower folder", p)
		}
	}
	fi, err := os.Stat(p)
	switch {
	case err != nil:
		return fmt.Errorf("%s does not exist", p)
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", p)
	}
	if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
		return nil // a repository root
	}
	for d := filepath.Dir(p); d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return fmt.Errorf("%s is inside the repository %s; map the repository itself", p, d)
		}
	}
	return nil
}

// Map sets the reference of a secret name for a repository or a folder
// above repositories, keyed by its resolved path. The name and reference are validated like a --secret flag.
func (c *Config) Map(repo, name, ref string) error {
	if why := compose.SecretNameProblem(name); why != "" {
		return fmt.Errorf("%s", why)
	}
	if err := compose.ValidateSecretRef(ref); err != nil {
		return fmt.Errorf("secret %s: %w", name, err)
	}
	p, err := identity.ResolvePath(repo)
	if err != nil {
		return err
	}
	if err := checkMapTarget(p); err != nil {
		return err
	}
	if c.Secrets == nil {
		c.Secrets = map[string]map[string]string{}
	}
	if c.Secrets[p] == nil {
		c.Secrets[p] = map[string]string{}
	}
	c.Secrets[p][name] = ref
	return nil
}

// Unmap removes the mapping of a secret name for a repository.
func (c *Config) Unmap(repo, name string) error {
	p, err := identity.ResolvePath(repo)
	if err != nil {
		return err
	}
	if _, ok := c.Secrets[p][name]; !ok {
		return fmt.Errorf("secret %s is not mapped for %s", name, p)
	}
	delete(c.Secrets[p], name)
	if len(c.Secrets[p]) == 0 {
		delete(c.Secrets, p)
	}
	return nil
}
