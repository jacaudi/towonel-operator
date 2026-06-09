{{/*
Build defaultPodOptions structure from flat values
*/}}
{{- define "towonel-operator.values.security" -}}
defaultPodOptions:
  # The operator requires its ServiceAccount token to reach the Kubernetes API.
  # common v5 no longer defaults this to true, so set it explicitly.
  automountServiceAccountToken: true
  enableServiceLinks: false
  hostIPC: false
  hostNetwork: false
  hostPID: false
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
{{- end -}}
