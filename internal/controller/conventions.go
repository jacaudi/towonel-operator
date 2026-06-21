package controller

import (
	"time"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

const (
	FinalizerName = "towonel-operator.towonel.io/finalizer"
	FieldOwner    = "towonel-operator"

	AnnotationInviteID   = "towonel.io/invite-id"
	AnnotationConfigHash = "towonel.io/config-hash"

	LabelPartOf = "app.kubernetes.io/part-of"
	PartOfValue = "towonel-operator"

	LabelAppName     = "app.kubernetes.io/name"
	LabelAppInstance = "app.kubernetes.io/instance"
	AgentAppName     = "towonel-agent"

	CondReady             = "Ready"
	CondInviteIssued      = "InviteIssued"
	CondHostnamesSynced   = "HostnamesSynced"
	CondTokenExpiringSoon = "TokenExpiringSoon"

	// Agent conditions (parent design §5.2).
	CondTunnelReady       = "TunnelReady"
	CondConfigRendered    = "ConfigRendered"
	CondPortsAllocated    = "PortsAllocated"
	CondWorkloadAvailable = "WorkloadAvailable"
	// Tunnel condition (parent design §5.1).
	CondPortsReserved = "PortsReserved"

	ReasonInvalidConfig = "InvalidConfig"
	ReasonReconciling   = "Reconciling"
	ReasonReady         = "Ready"
	ReasonAPIError      = "APIError"
	ReasonSynced        = "Synced"
	ReasonExpiringSoon  = "ExpiringSoon"
	ReasonNotExpiring   = "NotExpiring"

	ReasonTunnelNotFound     = "TunnelNotFound"
	ReasonTokenSecretMissing = "TokenSecretMissing"
	ReasonTokenStale         = "TokenStale"
	ReasonNoL4Services       = "NoL4Services"
	ReasonPending            = "Pending"
	ReasonRendered           = "Rendered"
	ReasonAvailable          = "Available"
	ReasonUnavailable        = "Unavailable"
	ReasonSecretClash        = "SecretClash"
	ReasonPortConflict       = "PortConflict"

	renewWindow     = 7 * 24 * time.Hour
	defaultTokenKey = "token"
	tokenDataKey    = "token"

	// waitingRequeue is the level-based fallback for the agent's not-ready
	// branches (design §3.3: cross-informer staleness can eat the watch wake-up).
	waitingRequeue = 30 * time.Second
	// hubCallTimeout bounds each Towonel hub call/step (design §3.1).
	hubCallTimeout = 20 * time.Second

	// defaultAgentImage is used when a TowonelAgent leaves spec.workload.image unset.
	// Pinned to a tag (never :latest) for reproducible rollouts; Renovate bumps it as
	// upstream tags new releases (see .github/renovate.json customManagers).
	defaultAgentImage = "codeberg.org/towonel/towonel-agent:0.1.32"
	agentHealthAddr   = "0.0.0.0:9090"
	agentHealthPort   = 9090

	// Connectivity (P6, design §4/§5/§8).
	CondIrohConnectivityReady  = "IrohConnectivityReady"
	ReasonConnectivityReady    = "ConnectivityReady"
	ReasonConnectivitySkipped  = "ConnectivitySkipped"  // invalid combo, non-wedging (design §4)
	ReasonNodeRBACShellMissing = "NodeRBACShellMissing" // chart shell absent (design §5.3)
	ReasonPortIgnored          = "NodePortPortIgnored"  // port set without create (design §4)

	// nodeReaderName is the fixed name of the chart-owned shared node-reader
	// ClusterRole + ClusterRoleBinding (design §5.3/§5.4).
	nodeReaderName = "towonel-operator-agent-node-reader"
)

func tokenSecretName(tunnelName string) string { return tunnelName + "-token" }

// portLabel is the hub-side reservation label for one tunnel l4 service.
func portLabel(tunnelNS, tunnelName, svc string) string {
	return tunnelNS + "/" + tunnelName + "/" + svc
}

// portLabelPrefix matches every reservation owned by one tunnel (leak sweep).
func portLabelPrefix(tunnelNS, tunnelName string) string {
	return tunnelNS + "/" + tunnelName + "/"
}

const (
	// Source annotation vocabulary (design §4; mirrors cloudflare-operator).
	AnnotationTunnel         = "towonel.io/tunnel"
	AnnotationTunnelRef      = "towonel.io/tunnel-ref"
	AnnotationAgentRef       = "towonel.io/agent-ref"
	AnnotationSrcHostname    = "towonel.io/hostname"
	AnnotationSrcOrigin      = "towonel.io/origin"
	AnnotationSrcEdgeTLSMode = "towonel.io/edge-tls-mode"
	AnnotationSrcProtocol    = "towonel.io/protocol"
	AnnotationGatewayService = "towonel.io/gateway-service"
	// AnnotationAutoRoutes opts a Gateway into auto-tunneling its SAME-NAMESPACE
	// child HTTPRoutes (issue #25). DISTINCT from AnnotationTunnel, which on a
	// Gateway means gateway-as-source (forward the Gateway's own listener
	// hostnames). Both may coexist on one Gateway. Requires AnnotationGatewayService.
	AnnotationAutoRoutes = "towonel.io/auto-routes"
	AnnotationDNSRecord  = "towonel.io/dns-record" // reserved (DNS phase); inert in P5

	// Operator-managed agent markers (design §5/§6).
	LabelManagedBy        = "app.kubernetes.io/managed-by"
	ManagedByValue        = "towonel-operator"
	AnnotationAutoCreated = "towonel.io/auto-created"

	// Source Event reasons (design §4/§5; Events-only validation).
	ReasonInvalidAnnotation   = "InvalidAnnotationValue"
	ReasonTunnelRefMissing    = "TunnelRefMissing"
	ReasonGatewayServiceUnset = "GatewayServiceUnspecified"
	ReasonHostnameConflict    = "HostnameConflict"
	ReasonAmbiguousGateway    = "AmbiguousGateway"
	ReasonAgentRefNotFound    = "AgentRefNotFound"
	ReasonAgentRefConflict    = "AgentRefConflict"
	ReasonDefaultAgentClash   = "DefaultAgentNameConflict"
	ReasonMultipleAgents      = "MultipleAgentsOnTunnel"
	ReasonObserveOnly         = "ObserveOnlyAgent"
	ReasonReconcilingAgent    = "ReconcilingAgent"
	// ReasonAutoSelectedByGateway is a Normal Event recorded on an HTTPRoute when it
	// is tunneled by inheriting a parent Gateway's towonel.io/auto-routes default
	// (issue #25) rather than its own towonel.io/tunnel annotation — exposure is
	// never silent.
	ReasonAutoSelectedByGateway = "AutoSelectedByGateway"
)

// srcFieldManager is the per-source SSA field manager owning that source's
// routing entries in the shared agent (design §5).
func srcFieldManager(kind, namespace, name string) string {
	return "towonel-src:" + kind + ":" + namespace + ":" + name
}

// agentSAName is the per-agent ServiceAccount name (matches the Deployment).
func agentSAName(agentName string) string { return agentName }

// nodePortServiceName resolves the per-agent UDP NodePort Service name.
func nodePortServiceName(ta *towonelv1alpha1.TowonelAgent) string {
	if n := ta.Spec.Connectivity.NodePort.Name; n != "" {
		return n
	}
	return ta.Name + "-iroh"
}

// servicesReaderName is the per-agent services-reader Role/RoleBinding name.
func servicesReaderName(agentName string) string { return agentName + "-iroh-svc-reader" }
