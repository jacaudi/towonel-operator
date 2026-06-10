{{- define "towonel-operator.values.controllers" -}}
controllers:
  main:
    type: deployment
    replicas: {{ .Values.replicas }}
    strategy: RollingUpdate
    serviceAccount:
      identifier: main
    containers:
      main:
        image:
          repository: {{ .Values.image.repository }}
          tag: {{ .Values.image.tag | default .Chart.AppVersion }}
          pullPolicy: {{ .Values.image.pullPolicy }}
        args:
          - --metrics-bind-address=:8080
          - --health-probe-bind-address=:8081
          - --leader-elect={{ .Values.leaderElection.enabled }}
          - --zap-log-level={{ .Values.logLevel }}
          - --towonel-api-url={{ .Values.towonel.apiURL }}
        env:
          POD_NAMESPACE:
            valueFrom:
              fieldRef:
                fieldPath: metadata.namespace
          {{- if .Values.credentials.existingSecret }}
          TOWONEL_API_KEY:
            valueFrom:
              secretKeyRef:
                name: {{ .Values.credentials.existingSecret }}
                key: {{ .Values.credentials.tokenKey }}
          {{- end }}
        probes:
          liveness:
            enabled: true
            custom: true
            spec:
              httpGet:
                path: /healthz
                port: 8081
          readiness:
            enabled: true
            custom: true
            spec:
              httpGet:
                path: /readyz
                port: 8081
        resources:
          {{- toYaml .Values.resources | nindent 10 }}
{{- end -}}
