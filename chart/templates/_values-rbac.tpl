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
        - apiGroups:
            - ""
          resources:
            - events
          verbs:
            - create
            - patch
        - apiGroups:
            - ""
          resources:
            - secrets
            - services
          verbs:
            - create
            - delete
            - get
            - list
            - patch
            - update
            - watch
        - apiGroups:
            - apps
          resources:
            - deployments
          verbs:
            - create
            - delete
            - get
            - list
            - patch
            - update
            - watch
        - apiGroups:
            - gateway.networking.k8s.io
          resources:
            - gateways
            - httproutes
            - referencegrants
          verbs:
            - get
            - list
            - watch
        - apiGroups:
            - towonel.io
          resources:
            - towonelagents
            - towoneltunnels
          verbs:
            - create
            - delete
            - get
            - list
            - patch
            - update
            - watch
        - apiGroups:
            - towonel.io
          resources:
            - towonelagents/finalizers
            - towoneltunnels/finalizers
          verbs:
            - update
        - apiGroups:
            - towonel.io
          resources:
            - towonelagents/status
            - towoneltunnels/status
          verbs:
            - get
            - patch
            - update
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
