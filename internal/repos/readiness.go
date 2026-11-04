package repos

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// ReadinessOf classifies a repo (design.md D3/D6a) from its checkouts (the
// main checkout first, then every worktree), the known instances and the
// profiles. Only verified learned knowledge makes a repo ready to start: an
// adapter, a prepared sibling instance or a profile row means it could run
// but nobody confirmed how (migrations, seed, login), so it needs learn.
// ref is the default branch a sibling's tracked compose files must exist on.
func ReadinessOf(name string, checkouts []string, ref string, insts []inventory.Instance, profileDirs []string) (level, reason string) {
	draft, sibling, broken := "", "", ""
	for _, c := range checkouts {
		ks, ok := inventory.ReadKnowledgeStatus(c)
		if !ok {
			if err := inventory.ReadKnowledgeError(c); err != nil && broken == "" {
				broken = "knowledge in " + filepath.Base(c) + " cannot be loaded: " + err.Error()
			}
			continue
		}
		switch ks.Status {
		case knowledge.StatusVerified:
			return Ready, "verified knowledge in " + filepath.Base(c)
		case knowledge.StatusStale:
			return Ready, "knowledge in " + filepath.Base(c) + " is stale (" + strings.Join(ks.ChangedFiles, ", ") + "); run stackfork learn"
		case knowledge.StatusDraft:
			if draft == "" {
				draft = "knowledge in " + filepath.Base(c) + " is a draft, not verified yet; finish stackfork learn"
			}
		}
	}
	if broken != "" {
		return NeedsLearn, broken
	}
	if draft != "" {
		return Draft, draft
	}
	const learnIt = "; run stackfork learn to verify how it starts"
	for _, c := range checkouts {
		for _, dir := range []string{".stackfork", ".agent-dev"} {
			if m, _ := filepath.Glob(filepath.Join(c, dir, "*.compose.yaml")); len(m) > 0 {
				return NeedsLearn, "adapter " + dir + "/" + filepath.Base(m[0]) + " in " + filepath.Base(c) + " but no verified knowledge" + learnIt
			}
		}
	}
	if len(checkouts) > 0 {
		r, err := FindSiblingRecipe(checkouts[0], "", ref, insts)
		if r != nil {
			return NeedsLearn, "prepared instance " + r.Project + " (" + filepath.Base(r.Source) + ") but no verified knowledge" + learnIt
		}
		if err != nil {
			sibling = "a prepared instance exists but cannot seed a new worktree: " + err.Error()
		}
	}
	if p := profileMention(name, profileDirs); p != "" {
		return NeedsLearn, "named by a profile reference (" + p + ") but no verified knowledge" + learnIt
	}
	if sibling != "" {
		return Unknown, sibling
	}
	return Unknown, "never learned: no knowledge, adapter, instance or profile reference"
}

// profileMention returns the profile reference that has a recipe row for
// the repo: a markdown table row whose first cell is the repo name, in any
// references/*.md file. context.md is personal instructions, not recipes, so
// a word there (e.g. "notes", "platform") never makes a repo ready.
func profileMention(name string, profileDirs []string) string {
	if len(name) < 3 {
		return ""
	}
	row := regexp.MustCompile(`(?im)^\|\s*` + regexp.QuoteMeta(name) + `\s*\|`)
	for _, d := range profileDirs {
		entries, err := os.ReadDir(filepath.Join(d, "references"))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(d, "references", e.Name())); err == nil && row.Match(b) {
				return "references/" + e.Name()
			}
		}
	}
	return ""
}

// Prompt is the text "Learn with your agent" copies (design.md D7).
func Prompt(name, path, branch string) string {
	where := "in a worktree created with `stackfork worktree add`"
	if at := checkedOutAt(path, branch); at != "" {
		// git refuses a second checkout of the branch: use the one that has it.
		where = fmt.Sprintf("in its existing checkout: the branch is already checked out at `%s`, so do not create a worktree; uncommitted changes there do not block stackfork", at)
	}
	return fmt.Sprintf("Using stackfork, run `stackfork learn` for `%s` (`%s`) on branch `%s` %s, then prepare and start it. "+
		"Record the dependency install command (`yarn install --frozen-lockfile`, `npm ci`, `pnpm install --frozen-lockfile`, `bun install`) as an exec step with `--once deps` (`--on host`, or in the container), before the build and migrate steps: it runs again only when a lockfile changes. Check migrations, seed data and login. Before you record a migrate or seed step, read the startup findings in `.stackfork/knowledge.md` and the first-start logs: if the container already migrates or seeds on start, do not record that step (say so in a note), and a seed step must not repeat the migrate step. Read the README, the docs and the Compose file for the local URLs that matter (the app, an admin or dashboard page, a mail UI, API docs, Storybook), check that each one answers, and record them with `stackfork learn record entry` and `learn record link`. If the project needs a credential stackfork cannot find (a private registry token, an API key), record its name with `stackfork learn record secret NAME --hint TEXT` and ask me to set it; never ask me to paste a value. Confirm hot reload: edit a visible string, check it changes without a restart, then revert the edit, and record the result with `stackfork learn record reload SERVICE hot|restart|rebuild`. Report `stackfork urls`, and once I confirm it works record it with `stackfork learn verify`. "+
		"Ask before building anything beyond the running instance.", name, path, branch, where)
}
