package cli

import (
	"strings"
	"testing"
)

// The confirmation of a cleanup names every ignored file or folder that the
// cleanup deletes, and how many, before the person confirms.
func TestCleanupDescribeCountsAndListsIgnoredFiles(t *testing.T) {
	p := CleanupPreview{Repo: "acme-web", Path: "/w/acme-web/.worktrees/feature-invoices", Branch: "feature-invoices", PR: 7,
		Volumes: []string{}, Ignored: []string{".env.local", "notes/", ".idea/"}}
	got := p.Describe()
	if !strings.Contains(got, "Ignored files that go with the worktree (3):\n  .env.local\n  notes/\n  .idea/") {
		t.Fatalf("missing the count and the list:\n%s", got)
	}
	p.Ignored = nil
	if strings.Contains(p.Describe(), "Ignored files") {
		t.Fatalf("no ignored files, no section:\n%s", p.Describe())
	}
}
