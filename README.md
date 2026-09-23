# Isolated development

Shared agent skill and Docker Compose helper for isolated development across projects, branches and Git worktrees. Works with any project’s Compose services, including dependency-only stacks and host-run apps. Includes an optional Node/Bun Nuxt template; ClientCo-specific recipes live in a separate reference and asset directory.

The maintained source lives in this repository. Local Codex and Claude Code skill directories link to it.

- [Agent instructions](SKILL.md)
- [Project-specific guidance](references/clientco.md)
- [Compose helper](scripts/compose.py)

Ask an agent: `Use $isolated-dev to prepare this project in a new worktree.`

## Reusing a prepared checkout

```sh
python3 scripts/compose.py --root "$CHECKOUT" prepare
python3 scripts/compose.py --root "$CHECKOUT" up
python3 scripts/compose.py --root "$CHECKOUT" urls
```

`prepare` reuses the last source-file selection, or the standard `.agent-dev/` template paths on first use. Select a project's authorized registry config once with `--npmrc "$PROJECT_NPMRC"` before `prepare`; subsequent starts reuse its path. Normal startup does not reset or seed the database.

`where` returns the instance hostname before startup; `urls` uses it for HTTP endpoints, for example `app-b-worktree-<hash>.localhost:<port>`. Select a shorter unique name with `prepare --hostname app-b-571.localhost`. Agents must use that same hostname in browser-facing app configuration; DB endpoints remain on `127.0.0.1`. Existing app adapters need their callback/CORS/HMR settings checked when adopting it.

The Nuxt template includes real Node 22 and Bun, defaults to one app replica, and supports `DEV_APP_REPLICAS=2` for multi-replica testing.

## Checks

```sh
python3 scripts/test_compose.py
```

Optional live checks create disposable Docker stacks and clean up their own resources:

```sh
python3 scripts/test_compose.py --live
python3 scripts/test_template.py
```

The first live check reads the local APP-A/APP-C/APP-B Compose definitions under `~/code/Agency/ClientCo`. The template check uses a synthetic Node streaming app and may download public images/packages. Neither establishes full private ClientCo application compatibility.

The repository lives at `~/code/me/isolated-dev`. The old repository/skill paths remain aliases; legacy Docker identities and state paths are retained to avoid orphaning existing stacks.
