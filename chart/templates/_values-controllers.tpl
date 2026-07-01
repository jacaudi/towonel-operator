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
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop:
              - ALL
        args:
          - --metrics-bind-address=:8080
          - --health-probe-bind-address=:8081
          - --leader-elect={{ .Values.leaderElection.enabled }}
          - --zap-log-level={{ .Values.logLevel }}
          - --towonel-api-url={{ .Values.towonel.apiURL }}
          - --enable-gateway-api={{ .Values.gatewayAPI.enabled }}
          {{- if .Values.agentNamespace }}
          - --agent-namespace={{ .Values.agentNamespace }}
          {{- end }}
          {{- if gt (int .Values.defaultAgent.replicas) 0 }}
          - --default-agent-replicas={{ .Values.defaultAgent.replicas }}
          {{- end }}
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
