#!/usr/bin/env bash
# Single source for code/manifest generation. Needs only go; controller-gen and yq are pinned go tools.
# Usage: hack/regenerate.sh [deepcopy|manifests|all]   (default: all)
# Called by taskfile.yml and by Renovate's postUpgradeTasks (.github/renovate.json).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

deepcopy() {
  go tool controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
}

manifests() {
  go tool controller-gen rbac:roleName=towonel-operator crd paths="./api/...;./internal/controller/..." output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
}

case "${1:-all}" in
  deepcopy)  deepcopy ;;
  manifests) manifests ;;
  all)
    deepcopy
    manifests
    ./hack/sync-helm-crds.sh
    ./hack/generate-helm-rbac.sh
    ;;
  *) echo "usage: $0 [deepcopy|manifests|all]" >&2; exit 2 ;;
esac
