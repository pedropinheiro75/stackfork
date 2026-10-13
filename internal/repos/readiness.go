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
	draft, sibling := "", ""
	for _, c := range checkouts {
		ks, ok := inventory.ReadKnowledgeStatus(c)
		if !ok {
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
	if draft != "" {
		return Draft, draft
	}
	const learnIt = "; run stackfork learn to verify how it starts"
	for _, c := range checkouts {
		for _, dir := range []string{".stackfork", ".agent-dev"} {
			if m, _ := filepath.Glob(filepath.Join(c, dir, "*.compose.yaml")); len(m) > 0 {
				return NeedsLearn, "adapter " + filepath.Join(dir, filepath.Base(m[0])) + " in " + filepath.Base(c) + " but no verified knowledge" + learnIt
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
	return fmt.Sprintf("Using stackfork, run `stackfork learn` for `%s` (`%s`) on branch `%s` in a worktree created with `stackfork worktree add`, then prepare and start it. "+
		"Check migrations, seed data and login, report `stackfork urls`, and once I confirm it works record it with `stackfork learn verify`. "+
		"Ask before building anything beyond the running instance.", name, path, branch)
}
