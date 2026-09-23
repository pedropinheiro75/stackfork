# ClientCo isolated development

Shared agent skill and Docker Compose helper for isolated development across projects, branches and Git worktrees. Includes APP-A-derived Nuxt templates and adaptation guidance for the ClientCo projects.

The maintained source lives in this repository. Local Codex and Claude Code skill directories link to it.

- [Agent instructions](SKILL.md)
- [Project-specific guidance](references/clientco.md)
- [Compose helper](scripts/compose.py)

Ask an agent: `Use $isolated-dev to prepare APP-A in a new worktree.`

## Checks

```sh
python3 scripts/test_compose.py
```

Optional live checks create disposable Docker stacks and clean up their own resources:

```sh
python3 scripts/test_compose.py --live
python3 scripts/test_template.py
```

The first live check reads the local APP-A/APP-C/APP-B Compose definitions under `~/code/Agency/ClientCo`. The template check uses a synthetic app and may download public images/packages. Neither establishes full private ClientCo application compatibility.
