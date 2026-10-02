package cli

import "os"

// Fixture repos must never trigger a signing prompt (a developer's global
// git config may sign commits through an agent such as 1Password): every
// git subprocess a test starts inherits these overrides.
func init() {
	for k, v := range map[string]string{
		"GIT_CONFIG_COUNT":   "2",
		"GIT_CONFIG_KEY_0":   "commit.gpgsign",
		"GIT_CONFIG_VALUE_0": "false",
		"GIT_CONFIG_KEY_1":   "tag.gpgsign",
		"GIT_CONFIG_VALUE_1": "false",
	} {
		os.Setenv(k, v)
	}
}
