# Contributing

Thanks for your interest in the Towonel Operator. This document covers the conventions
that the release tooling and CI depend on.

## Commit convention

Commits follow [Conventional Commits](https://www.conventionalcommits.org/). The release
pipeline (release-please, `release-please-config.json`) maintains a release PR from commit
types on `main`; merging that PR tags the release and publishes the image and chart:

- `feat:` — a new feature (minor release)
- `fix:` / `perf:` — a bug fix or performance improvement (patch release)
- `refactor:` — internal change, no behavior change (patch release)
- `deps(towonel-agent):` — default agent image bump from Renovate (patch release)
- `chore:`, `chore(deps):`, `docs:`, `ci:`, `test:`, `build:`, `style:` — no release on their own

## Versioning (alpha)

This project is in **alpha**. Breaking changes are expected and warrant only a **minor**
version bump — they ship as `feat:` commits. release-please has no `breaking → minor` rule
for versions ≥ 1.0, so a `!`/`BREAKING CHANGE`-marked commit **would** propose a major
release: do not use those markers while in alpha. If one lands anyway, push a commit with a
`Release-As: x.y.0` footer to override the proposed version. This policy will be revisited
when the project leaves alpha.

## Pull requests from forks

CI runs on `pull_request` for fork PRs so they report the required `ci` check, but fork runs
are **test only**: `task ci` (lint, tests, drift gate) with a read-only token and no
secrets. No image is built or smoke-tested and nothing is published; a maintainer's branch
covers that before release. A maintainer may need to approve the first run for a
first-time contributor.

## Generated artifacts

CRDs, RBAC, and deepcopy code are generated from kubebuilder markers — never hand-edit the
outputs. After changing markers or API types, regenerate and commit the results:

```sh
task generate manifests sync-helm-crds generate-helm-rbac
```

CI enforces this with a drift gate (`task verify-generated`); a stale generated artifact
fails the build.
