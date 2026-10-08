# Worktrees, identity and uncommitted work

For concurrent agents, use separate Git worktrees. Derive identity from the full checkout path AND branch, never basename alone. Stop the old stack **before** switching branches in the same folder: running bind mounts immediately see changed files, and a branch-specific Docker name cannot isolate shared source files on its own.

`up`/`run` refuse to start while another managed branch stack is running from the same checkout folder (whether it was prepared under the current identity or a legacy one) — this cannot undo source changes an already-running bind mount has already seen. Use `stackfork where` to see the other prepared instance, then `stackfork down --instance NAME` to stop it before starting this branch. Stop an instance before removing its worktree.

## Carrying uncommitted work

When the requested app includes uncommitted work, carry the requested tracked changes and relevant untracked source files into the new worktree. Preserve binaries, workspace packages and patches; exclude ignored credentials, dependencies and build output. Verify the original checkout is unchanged afterward. A worktree created from HEAD alone does not contain that work — copy or `git worktree add` plus a targeted `git stash`/patch apply, whichever the checkout's state needs.

## Generated files stay out of tracked source

Keep the adapter's generated files in the checkout's `.agent-dev/`, excluded using Git's local exclude file (`.git/info/exclude`); do not overwrite tracked Compose files or the developer's real `.env`. New worktrees do not contain ignored files, so start configuration from the project's own examples and fresh local values. This is unlike `.stackfork/` (`references/learning.md`), which holds committed project knowledge and is meant to be shared.

Never open another project's real env files, registry credentials, certificates or private keys, and never copy another project's credentials into this one — obtain registry access through the approved credential source for this project (`references/adapting-compose.md`).

Create the worktree with `stackfork worktree add REPO BRANCH` (`<repo>/.worktrees/<branch>`, excluded locally from git; it copies only the adapter files the project's knowledge lists, never `.env` or credentials). `stackfork repos` lists every repo, branch and worktree under the configured folders; `stackfork start-env CHECKOUT` prepares and starts a known project's checkout.
