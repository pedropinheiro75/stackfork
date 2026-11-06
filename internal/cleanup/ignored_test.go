package cleanup

import (
	"slices"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func autoWarning(t *testing.T, f fixture, c Candidate, r Result, chk func(*Check)) Verdict {
	t.Helper()
	k := Check{Main: f.Main, Candidate: c, Result: r, Auto: true}
	if chk != nil {
		chk(&k)
	}
	return Evaluate(k)
}

// Files that the manifest records for the instance are stackfork's own: they
// do not block `auto`. A file of a person in the same folder still does, and
// the folder is never excluded as a whole.
func TestEvaluateOwnFilesDoNotBlockAutoButAPersonsFileInTheSameFolderDoes(t *testing.T) {
	f, c, r := mergedFx(t)
	ignore(t, c.Path, ".agent-dev/runtime.env")
	put(t, c.Path, ".agent-dev/nuxt.compose.yaml", "services: {}\n")
	own := func(k *Check) { k.Own = []string{".agent-dev/runtime.env", ".agent-dev/nuxt.compose.yaml"} }
	if v := autoWarning(t, f, c, r, own); !v.Eligible || len(v.Ignored) != 0 {
		t.Fatalf("only stackfork's own files: %+v", v)
	}
	put(t, c.Path, ".agent-dev/notes.md", "mine\n")
	v := autoWarning(t, f, c, r, own)
	if v.Eligible || !strings.Contains(v.Warning, ".agent-dev/notes.md") || strings.Contains(v.Warning, "runtime.env") {
		t.Fatalf("a person's file in .agent-dev must block: %+v", v)
	}
	if want := []string{".agent-dev/notes.md"}; !slices.Equal(v.Ignored, want) {
		t.Fatalf("ignored %v, want %v", v.Ignored, want)
	}
	// without the record the same folder blocks as a whole, as before
	if v := autoWarning(t, f, c, r, nil); v.Eligible {
		t.Fatalf("no record, no exclusion: %+v", v)
	}
}

// The .gitignore that `learn` writes counts only with the exact content it
// writes; the knowledge files only when they equal the main checkout's copy.
func TestEvaluateGeneratedStackforkFilesCountOnlyWhenTheyAreWhatStackforkWrote(t *testing.T) {
	f, c, r := mergedFx(t)
	excludeLine(t, c.Path, "/.stackfork")
	put(t, c.Path, ".stackfork/.gitignore", knowledge.StackforkGitignore)
	put(t, f.Main, ".stackfork/knowledge.json", "{}\n")
	put(t, c.Path, ".stackfork/knowledge.json", "{}\n")
	if v := autoWarning(t, f, c, r, nil); !v.Eligible {
		t.Fatalf("generated files: %+v", v)
	}
	put(t, c.Path, ".stackfork/.gitignore", knowledge.StackforkGitignore+"secrets/\n")
	if v := autoWarning(t, f, c, r, nil); v.Eligible || !strings.Contains(v.Warning, ".stackfork/.gitignore") {
		t.Fatalf("an edited .gitignore is a person's: %+v", v)
	}
	put(t, c.Path, ".stackfork/.gitignore", knowledge.StackforkGitignore)
	put(t, c.Path, ".stackfork/knowledge.json", `{"plan": "edited here"}`)
	if v := autoWarning(t, f, c, r, nil); v.Eligible || !strings.Contains(v.Warning, ".stackfork/knowledge.json") {
		t.Fatalf("a knowledge file that differs from main's is work: %+v", v)
	}
}

// A person's own ignored files still block `auto` and are in the list that
// the manual confirmation shows.
func TestEvaluatePersonsIgnoredFilesBlockAutoAndAreListed(t *testing.T) {
	f, c, r := mergedFx(t)
	ignore(t, c.Path, ".env.local")
	ignore(t, c.Path, "notes/todo.md")
	ignore(t, c.Path, ".turbo/cache/x")
	v := autoWarning(t, f, c, r, nil)
	if v.Eligible || !v.IgnoredOnly || !strings.Contains(v.Warning, ".env.local") || !strings.Contains(v.Warning, "notes/") {
		t.Fatalf("auto: %+v", v)
	}
	m := Evaluate(Check{Main: f.Main, Candidate: c, Result: r})
	if !m.Eligible || m.IgnoredOnly || !slices.Equal(m.Ignored, []string{".env.local", "notes/"}) {
		t.Fatalf("manual: %+v", m)
	}
}

// Ignored files are the only blocker only when nothing else blocks.
func TestEvaluateIgnoredOnlyIsFalseWhenSomethingElseBlocks(t *testing.T) {
	f, c, r := mergedFx(t)
	ignore(t, c.Path, ".env.local")
	v := autoWarning(t, f, c, r, func(k *Check) { k.Locked = func() string { return "a run holds the instance's run lock" } })
	if v.Eligible || v.IgnoredOnly || !strings.Contains(v.Warning, "run lock") {
		t.Fatalf("locked: %+v", v)
	}
	put(t, c.Path, "app.txt", "edited\n")
	if v := autoWarning(t, f, c, r, nil); v.IgnoredOnly {
		t.Fatalf("dirty: %+v", v)
	}
}

func TestEvaluateToolCachesAndBuildOutputsAtKnownRootsDoNotBlockAuto(t *testing.T) {
	names := []string{".turbo", ".yarn", ".pnpm-store", ".parcel-cache", ".svelte-kit", ".angular", ".pytest_cache", "__pycache__",
		".mypy_cache", ".ruff_cache", ".gradle", "coverage", "dist", "build"}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			f, c, r := mergedFxWith(t, "web/package.json", "{}")
			ignore(t, c.Path, n+"/x/y.bin")
			if v := autoWarning(t, f, c, r, nil); !v.Eligible {
				t.Fatalf("%s at the root: %+v", n, v)
			}
			excludeLine(t, c.Path, "/web/"+n)
			put(t, c.Path, "web/"+n+"/z.bin", "x")
			if v := autoWarning(t, f, c, r, nil); !v.Eligible {
				t.Fatalf("%s beside a manifest: %+v", n, v)
			}
			excludeLine(t, c.Path, "/src/"+n)
			put(t, c.Path, "src/"+n+"/z.bin", "x")
			if v := autoWarning(t, f, c, r, nil); v.Eligible {
				t.Fatalf("src/%s is not at a known root: %+v", n, v)
			}
		})
	}
}

// The cache rule is about ignored files. A tracked file in dist/ is not
// ignored: editing it is an uncommitted change, as always.
func TestEvaluateTrackedFileInABuildFolderIsStillWork(t *testing.T) {
	f, c, r := mergedFxWith(t, "dist/keep.js", "1\n")
	ignore(t, c.Path, "dist/new.js")
	if v := autoWarning(t, f, c, r, nil); !v.Eligible {
		t.Fatalf("an ignored file beside a tracked one: %+v", v)
	}
	put(t, c.Path, "dist/keep.js", "2\n")
	v := autoWarning(t, f, c, r, nil)
	if v.Eligible || !strings.Contains(v.Warning, "uncommitted") {
		t.Fatalf("%+v", v)
	}
}
