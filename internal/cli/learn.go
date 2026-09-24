package cli

import (
	"io"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/learn"
)

// learnOutput summarises a scan; never includes an env value, a script's
// full command text where it might quote one, or an env file's contents -
// only counts and the paths written.
type learnOutput struct {
	Root         string `json:"root"`
	Status       string `json:"status"`
	ComposeFiles int    `json:"compose_files"`
	Services     int    `json:"services"`
	Refusals     int    `json:"isolation_findings"`
	Risks        int    `json:"risks"`
	EnvKeys      int    `json:"env_keys"`
	Written      string `json:"written"`
}

// cmdLearn runs the deterministic scan (task 13.2) and writes
// .stackfork/knowledge.json, .stackfork/knowledge.md and
// .stackfork/.gitignore.
func cmdLearn(stdout io.Writer, root string) error {
	k, err := learn.Scan(root)
	if err != nil {
		return err
	}
	if err := learn.WriteKnowledge(root, k); err != nil {
		return err
	}
	return printJSON(stdout, learnOutput{
		Root:         root,
		Status:       k.Status,
		ComposeFiles: len(k.Facts.ComposeFiles),
		Services:     len(k.Facts.Services),
		Refusals:     len(k.Refusals),
		Risks:        len(k.Risks),
		EnvKeys:      len(k.EnvKeys),
		Written:      filepath.Join(root, ".stackfork"),
	})
}
