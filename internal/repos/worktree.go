package repos

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// WorktreeResult describes what AddWorktree did.
type WorktreeResult struct {
	Path    string
	Branch  string
	Copied  []string // adapter files copied from the main checkout
	Skipped []string // listed by knowledge but refused (credentials) or missing
}

// ValidBranch checks a branch name the way git does, and refuses anything
// that could be read as an option.
func ValidBranch(name string) error {
	if name == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid branch name %q", name)
	}
	if err := exec.Command("git", "check-ref-format", "--branch", name).Run(); err != nil {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

// WorktreeDir is where a branch's worktree lives: <repo>/.worktrees/<branch
// with slashes turned into dashes>.
func WorktreeDir(repo, branch string) string {
	return filepath.Join(repo, ".worktrees", strings.ReplaceAll(branch, "/", "-"))
}

func gitRun(repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func refExists(repo, ref string) bool {
	_, err := gitRun(repo, "show-ref", "--verify", "--quiet", ref)
	return err == nil
}

// AddWorktree creates <repo>/.worktrees/<branch> (design.md D5). repo must
// be a main checkout. An existing local branch is checked out; a branch that
// exists only as origin/<branch> gets a tracking branch; a new branch needs
// an explicit from. Never touches the main checkout's working tree.
func AddWorktree(repo, branch, from string) (WorktreeResult, error) {
	res := WorktreeResult{Branch: branch}
	if fi, err := os.Stat(filepath.Join(repo, ".git")); err != nil || !fi.IsDir() {
		return res, fmt.Errorf("%s is not a main git checkout", repo)
	}
	if err := ValidBranch(branch); err != nil {
		return res, err
	}
	if from != "" && (strings.HasPrefix(from, "-") || exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", from+"^{commit}").Run() != nil) {
		return res, fmt.Errorf("--from %q is not a commit in %s", from, repo)
	}
	dest := WorktreeDir(repo, branch)
	if _, err := os.Lstat(dest); err == nil {
		return res, fmt.Errorf("%s already exists; nothing changed", dest)
	}
	var args []string
	switch {
	case refExists(repo, "refs/heads/"+branch):
		args = []string{"worktree", "add", dest, branch}
	case refExists(repo, "refs/remotes/origin/"+branch):
		args = []string{"worktree", "add", "-b", branch, dest, "origin/" + branch}
	case from != "":
		args = []string{"worktree", "add", "-b", branch, dest, from}
	default:
		return res, fmt.Errorf("branch %q is not a local branch or on origin (as last fetched); pass --from BASE to create it", branch)
	}
	if out, err := gitRun(repo, args...); err != nil {
		return res, fmt.Errorf("git %s: %s", strings.Join(args[:2], " "), out)
	}
	res.Path = dest
	if r, err := filepath.EvalSymlinks(dest); err == nil {
		res.Path = r
	}
	if err := excludeWorktrees(repo); err != nil {
		return res, err
	}
	res.Copied, res.Skipped = copyAdapterFiles(repo, dest)
	return res, nil
}

// excludeWorktrees adds /.worktrees/ to the repo's local git exclude.
func excludeWorktrees(repo string) error {
	p, err := gitRun(repo, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return fmt.Errorf("locating info/exclude: %s", p)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(repo, p)
	}
	cur, _ := os.ReadFile(p)
	for _, l := range strings.Split(string(cur), "\n") {
		if strings.TrimSpace(l) == "/.worktrees/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(cur) > 0 && !bytes.HasSuffix(cur, []byte("\n")) {
		prefix = "\n"
	}
	_, err = io.WriteString(f, prefix+"/.worktrees/\n")
	return err
}

// copyAdapterFiles copies the files the project's knowledge lists as
// adapter files from the main checkout into the worktree, never a file the
// credential denylist matches and never over an existing file.
// ponytail: profiles carry no structured file list, so only knowledge's
// adapter_files is read.
func copyAdapterFiles(repo, dest string) (copied, skipped []string) {
	k, err := knowledge.Load(filepath.Join(repo, ".stackfork", "knowledge.json"))
	if err != nil {
		return nil, nil
	}
	for _, rel := range k.AdapterFiles {
		rel = filepath.Clean(rel)
		if filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			skipped = append(skipped, rel+" (outside the project)")
			continue
		}
		c, s := copyTree(filepath.Join(repo, rel), filepath.Join(dest, rel), rel)
		copied, skipped = append(copied, c...), append(skipped, s...)
	}
	return copied, skipped
}

func copyTree(src, dst, rel string) (copied, skipped []string) {
	fi, err := os.Lstat(src)
	switch {
	case err != nil:
		return nil, []string{rel + " (missing)"}
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, []string{rel + " (symlink)"}
	case fi.IsDir():
		entries, _ := os.ReadDir(src)
		for _, e := range entries {
			c, s := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), filepath.Join(rel, e.Name()))
			copied, skipped = append(copied, c...), append(skipped, s...)
		}
		return copied, skipped
	case !fi.Mode().IsRegular():
		return nil, nil
	}
	if why := Denied(rel, src); why != "" {
		return nil, []string{rel + " (" + why + ")"}
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil, []string{rel + " (already in the worktree)"}
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, []string{rel + " (unreadable)"}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, []string{rel + " (cannot create folder)"}
	}
	if err := os.WriteFile(dst, data, fi.Mode().Perm()); err != nil {
		return nil, []string{rel + " (write failed)"}
	}
	return []string{rel}, nil
}

var deniedExt = map[string]bool{
	".pem": true, ".key": true, ".p8": true, ".p12": true, ".pfx": true, ".crt": true,
	".cer": true, ".jks": true, ".keystore": true, ".gpg": true, ".asc": true,
}

// Denied returns why a file must never be copied into a worktree ("" when
// it may be): env files, key and certificate files, credential-looking
// names and token-bearing .npmrc files. path is read only for .npmrc.
func Denied(rel, path string) string {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".ssh" || seg == ".aws" || seg == ".gnupg" {
			return "credentials"
		}
	}
	base := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.HasPrefix(base, ".env"):
		return "env file"
	case deniedExt[filepath.Ext(base)] || strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") ||
		strings.HasPrefix(base, "id_ecdsa") || strings.HasPrefix(base, "id_dsa"):
		return "key or certificate"
	case base == ".netrc" || base == ".pgpass" || base == ".htpasswd" ||
		strings.Contains(base, "secret") || strings.Contains(base, "credential"):
		return "credentials"
	case strings.HasSuffix(base, ".npmrc"):
		b, _ := os.ReadFile(path)
		low := strings.ToLower(string(b))
		if strings.Contains(low, "_auth") || strings.Contains(low, "_password") || strings.Contains(low, "token") {
			return "token in .npmrc"
		}
	}
	return ""
}
