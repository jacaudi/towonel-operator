{{/*
Build serviceAccount structure from flat values
*/}}
{{- define "towonel-operator.values.serviceaccount" -}}
serviceAccount:
  main:
    enabled: {{ .Values.serviceAccount.create }}
    {{- with .Values.serviceAccount.annotations }}
    annotations:
      {{- toYaml . | nindent 6 }}
    {{- end }}
{{- end -}}
