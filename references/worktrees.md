# Worktrees, identity and uncommitted work

For concurrent agents, use separate Git worktrees. Derive identity from the full checkout path AND branch, never basename alone. Stop the old stack **before** switching branches in the same folder: running bind mounts immediately see changed files, and a branch-specific Docker name cannot isolate shared source files on its own.

`up`/`run` refuse to start while another managed branch stack is running from the same checkout folder (whether it was prepared under the current identity or a legacy one) — this cannot undo source changes an already-running bind mount has already seen. Use `stackfork where` to see the other prepared instance, then `stackfork down --instance NAME` to stop it before starting this branch. Stop an instance before removing its worktree.

## Carrying uncommitted work

When the requested app includes uncommitted work, carry the requested tracked changes and relevant untracked source files into the new worktree. Preserve binaries, workspace packages and patches; exclude ignored credentials, dependencies and build output. Verify the original checkout is unchanged afterward. A worktree created from HEAD alone does not contain that work — copy or `git worktree add` plus a targeted `git stash`/patch apply, whichever the checkout's state needs.

## Generated files stay out of tracked source

Keep the adapter's generated files in the checkout's `.agent-dev/`, excluded using Git's local exclude file (`.git/info/exclude`); do not overwrite tracked Compose files or the developer's real `.env`. New worktrees do not contain ignored files, so start configuration from the project's own examples and fresh local values. This is unlike `.stackfork/` (`references/learning.md`), which holds committed project knowledge and is meant to be shared.

Never open another project's real env files, registry credentials, certificates or private keys, and never copy another project's credentials into this one — obtain registry access through the approved credential source for this project (`references/adapting-compose.md`).

Create the worktree with `stackfork worktree add REPO BRANCH` (`<repo>/.worktrees/<branch>`, excluded locally from git; it copies only the adapter files the project's knowledge lists, never `.env` or credentials). `stackfork repos` lists every repo, branch and worktree under the configured folders; `stackfork start-env CHECKOUT` prepares and starts a known project's checkout. A branch that is already checked out (for example the default branch in the main checkout) gets no second worktree: `worktree add` names that checkout and the `start-env` command for it. Every start begins with a `sync` step that fast-forwards a clean checkout from its upstream and otherwise only warns.

## A worktree prepared with fewer files than the main checkout

A worktree can run without a Compose file the main checkout uses. The plan's `prepare` step may name only the override, or the worktree may have been prepared by hand with one `-f`. Once the worktree's manifest records the short list, every later `up` and `reset` keeps it, and the worktree behaves differently from the main checkout (for example, the base file that publishes the app port is missing).

stackfork says so when it happens: `prepare`, `start-env`, `reset` and the dashboard show `warning: this worktree prepares with N Compose file(s) (...); the main checkout's last prepare used M (...). Missing: FILE. To reuse the main checkout's recipe: stackfork --root WORKTREE reset --from-main`. It compares only the set of Compose files with the main checkout's last prepare. It never blocks anything, because a lighter recipe can be on purpose.

To fix it, run `stackfork --root WORKTREE reset --from-main` (a person runs it; the guard blocks `reset` for agents). It deletes the worktree's volumes, moves its untracked adapter files to `.stackfork-aside/<UTC time>/` inside the worktree, copies the main checkout's recipe and prepares with it. Nothing is deleted; remove `.stackfork-aside/<time>` when you no longer need it. To keep the next worktree from having the gap, run `stackfork learn verify` in a checkout whose instance has the full recipe: it writes that recipe to the plan's `prepare` step.

## Resetting and removing a worktree

- `stackfork reset` deletes this worktree's environment, volumes included, and starts it again from the code in the worktree. It never touches your code. A person runs it; the guard blocks it for agents.
- Live mode (`stackfork live on`) can offer to clean up a worktree after its branch is merged into the default branch or its pull request is closed. The cleanup removes the worktree, the instance with its volumes and, when you allow it, the local branch. `stackfork live clean [REPO] BRANCH` previews it, and only a person runs it with `--yes`. See `references/commands.md`.
- Stop an instance with `stackfork down` (data kept) before you remove its worktree by hand. `stackfork gc` finds instances whose checkout is gone.
