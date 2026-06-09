#!/usr/bin/env bash
# Converts kubebuilder-generated RBAC (config/rbac/role.yaml) into the bjw-s
# common-chart format used by chart/templates/_values-rbac.tpl.
# The kubebuilder markers in controller code are the source of truth.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
INPUT_FILE="${REPO_ROOT}/config/rbac/role.yaml"
OUTPUT_FILE="${REPO_ROOT}/chart/templates/_values-rbac.tpl"

if [[ ! -f "${INPUT_FILE}" ]]; then
  echo "Error: ${INPUT_FILE} not found. Run 'task manifests' first." >&2
  exit 1
fi
if ! command -v yq &>/dev/null; then
  echo "Error: yq is required (brew install yq)." >&2
  exit 1
fi

cat > "${OUTPUT_FILE}" << 'HEADER'
{{/*
RBAC for towonel-operator
AUTO-GENERATED from config/rbac/role.yaml - DO NOT EDIT MANUALLY
Run 'task generate-helm-rbac' to regenerate after updating kubebuilder markers.
*/}}
{{- define "towonel-operator.values.rbac" -}}
{{- if .Values.rbac.enabled }}
rbac:
  roles:
    main:
      enabled: true
      type: ClusterRole
      rules:
HEADER

yq eval '.rules' "${INPUT_FILE}" | sed 's/^/        /' >> "${OUTPUT_FILE}"

cat >> "${OUTPUT_FILE}" << 'FOOTER'
  bindings:
    main:
      enabled: true
      type: ClusterRoleBinding
      roleRef:
        identifier: main
      subjects:
        - identifier: main
{{- end }}
{{- end -}}
FOOTER

echo "Generated ${OUTPUT_FILE} from ${INPUT_FILE}"
