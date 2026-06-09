{{/*
Build service structure from flat values
*/}}
{{- define "towonel-operator.values.service" -}}
service:
  main:
    controller: main
    ports:
      metrics:
        port: 8080
        protocol: TCP
{{- end -}}
