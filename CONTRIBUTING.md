# Contributing

Thanks for your interest in the Towonel Operator. This document covers the conventions
that the release tooling and CI depend on.

## Commit convention

Commits follow [Conventional Commits](https://www.conventionalcommits.org/). The release
pipeline (`.releaserc.json`, semantic-release) derives the next version and the changelog
from commit types:

- `feat:` — a new feature (minor release)
- `fix:` — a bug fix (patch release)
- `refactor:` — internal change, no behavior change (patch release)
- `chore(deps):` / `chore(towonel-agent):` — dependency bumps (patch release)

## Versioning (alpha)

This project is in **alpha**. Breaking changes are expected and warrant only a **minor**
version bump — they ship as `feat:` commits. The release pipeline (`.releaserc.json`) maps
`breaking → minor`, so even a `!`/`BREAKING CHANGE`-marked commit still produces a minor
release. Do **not** treat API or behavior changes as major while in alpha. This policy will
be revisited when the project leaves alpha.

## Generated artifacts

CRDs, RBAC, and deepcopy code are generated from kubebuilder markers — never hand-edit the
outputs. After changing markers or API types, regenerate and commit the results:

```sh
task generate manifests sync-helm-crds generate-helm-rbac
```

CI enforces this with a drift gate (`task verify-generated`); a stale generated artifact
fails the build.
