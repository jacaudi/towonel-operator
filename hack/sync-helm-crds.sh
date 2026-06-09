#!/usr/bin/env bash
# Wraps config/crd/bases CRDs into chart templates gated by crds.install / crds.keep.
# config/crd/bases/ is the single source of truth; these files are generated — DO NOT EDIT.
# Run 'task sync-helm-crds' to regenerate after updating API types.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

for kind in towoneltunnels towonelagents; do
  src="${REPO_ROOT}/config/crd/bases/towonel.io_${kind}.yaml"
  dst="${REPO_ROOT}/chart/templates/crd-${kind}.yaml"

  if [[ ! -f "${src}" ]]; then
    echo "Error: ${src} not found. Run 'task manifests' first." >&2
    exit 1
  fi

  {
    printf '{{- if .Values.crds.install }}\n'
    awk '/controller-gen\.kubebuilder\.io\/version:/ {
      print $0
      print "{{- if .Values.crds.keep }}"
      print "    helm.sh/resource-policy: keep"
      print "{{- end }}"
      next
    } {print}' "${src}"
    printf '{{- end }}\n'
  } > "${dst}"

  grep -q '{{- if .Values.crds.install }}' "${dst}" \
    || { echo "ERROR: install gate missing in ${dst}" >&2; exit 1; }
  grep -q 'helm.sh/resource-policy: keep' "${dst}" \
    || { echo "ERROR: resource-policy missing in ${dst}" >&2; exit 1; }

  echo "Generated ${dst} from ${src}"
done
