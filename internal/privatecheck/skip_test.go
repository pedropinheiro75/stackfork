package privatecheck

import "testing"

func TestSkippedDir(t *testing.T) {
	for rel, want := range map[string]bool{
		".worktrees": true, "dist": true, ".git": true, ".superpowers": true,
		"internal/.worktrees": false, "internal": false,
	} {
		if got := skippedDir(rel); got != want {
			t.Errorf("skippedDir(%q) = %v, want %v", rel, got, want)
		}
	}
}
