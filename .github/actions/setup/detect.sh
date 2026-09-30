#!/usr/bin/env bash
# Detect which languages and artefacts a repo contains. Prints key=value lines.
#
# Shared by action.yml (which appends this to $GITHUB_OUTPUT) and by
# tests/detect-test.sh. ONE implementation so the test cannot drift from what
# actually ships.
#
# Usage: detect.sh [DIR]   (defaults to $PWD)
#
# THREE TRAPS ARE ENCODED HERE, each of which returns a confidently empty or
# wrong answer rather than erroring:
#
#   1. `-mindepth 1` is REQUIRED. The basename of the starting point `.` matches
#      the `.*` prune glob, so without it find prunes the entire tree and prints
#      nothing — every language silently undetected, in every repo.
#
#   2. The Dockerfile extension filter is anchored to `/Dockerfile.`. A blanket
#      `\.(json|md|txt)$` also strips every package.json, so Node can never be
#      detected.
#
#   3. The caller's expression is wrapped in \( \) inside find_real. A bare `-o`
#      binds looser than `-type f` and the `-prune` clause and discards both.
set -euo pipefail

cd "${1:-$PWD}"

pruned=(-name node_modules -o -name vendor -o -name .git -o -name .worktrees -o -name '.*')
find_real() {
  find . -mindepth 1 \( "${pruned[@]}" \) -prune -o -type f \( "$@" \) -print 2>/dev/null
}

# SHALLOWEST path wins, not the lexically first. A repo with example or test
# modules (examples/analytics/go.mod) would otherwise beat the real ./go.mod,
# because "./e" sorts before "./g" — and go-version-file would then point at a
# nested module's Go directive.
shallowest() { awk '{ n = gsub("/", "/"); print n, $0 }' | sort -k1,1n -k2,2 | head -1 | cut -d' ' -f2-; }

gomod=$(find_real -name go.mod | shallowest || true)
node=$(find_real -name package.json | sort | head -1 || true)
lock=$(find_real -name package-lock.json | sort | head -1 || true)
py=$(find_real -name pyproject.toml -o -name requirements.txt | sort | head -1 || true)
rust=$(find_real -name Cargo.toml | sort | head -1 || true)
chart=$(find_real -name Chart.yaml | sort | head -1 || true)
docker=$(find_real -name Dockerfile -o -name 'Dockerfile.*' \
           | grep -Ev '/Dockerfile\.(json|md|txt|ya?ml|lock)$' | sort | head -1 || true)
wf=$(find .github/workflows -maxdepth 1 -type f \( -name '*.yml' -o -name '*.yaml' \) 2>/dev/null | head -1 || true)

b() { [ -n "$1" ] && echo true || echo false; }

echo "gomod=${gomod#./}"
echo "go=$(b "${gomod}")"
echo "node=$(b "${node}")"
echo "nodelock=$(b "${lock}")"
echo "python=$(b "${py}")"
echo "rust=$(b "${rust}")"
echo "chart=$(b "${chart}")"
echo "docker=$(b "${docker}")"
echo "workflows=$(b "${wf}")"
# service = publishes a container image; library = does not.
echo "variant=$([ -n "${docker}" ] && echo service || echo library)"
