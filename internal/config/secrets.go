package config

import (
	"fmt"
	"sort"

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
			if !compose.ValidSecretName(name) {
				return fmt.Errorf("secrets for %s: a secret name must match [A-Za-z_][A-Za-z0-9_]*", repo)
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

// Map sets the reference of a secret name for a repository, keyed by its
// resolved path. The name and reference are validated like a --secret flag.
func (c *Config) Map(repo, name, ref string) error {
	if !compose.ValidSecretName(name) {
		return fmt.Errorf("secret name must match [A-Za-z_][A-Za-z0-9_]*")
	}
	if err := compose.ValidateSecretRef(ref); err != nil {
		return fmt.Errorf("secret %s: %w", name, err)
	}
	p, err := identity.ResolvePath(repo)
	if err != nil {
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
