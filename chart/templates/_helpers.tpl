{{/*
Return the full name for the chart
*/}}
{{- define "towonel-operator.fullname" -}}
{{- include "bjw-s.common.lib.chart.names.fullname" . -}}
{{- end -}}

{{/*
Return the chart name
*/}}
{{- define "towonel-operator.name" -}}
{{- include "bjw-s.common.lib.chart.names.name" . -}}
{{- end -}}
