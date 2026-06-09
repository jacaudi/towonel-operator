{{/*
Build serviceMonitor structure from flat values
*/}}
{{- define "towonel-operator.values.servicemonitor" -}}
{{- if .Values.observability.metrics.serviceMonitor.enabled }}
serviceMonitor:
  main:
    enabled: true
    serviceName: main
    endpoints:
      - port: metrics
        scheme: http
        path: /metrics
        interval: {{ .Values.observability.metrics.serviceMonitor.interval }}
{{- end }}
{{- end -}}
