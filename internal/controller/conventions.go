package controller

import (
	"time"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

const (
	FinalizerName = "towonel-operator.towonel.io/finalizer"
	FieldOwner    = "towonel-operator"

	// DefaultTowonelAPIURL is the public Towonel hub — the --towonel-api-url
	// default, and the only hub for which publicHubEdgeTargets apply.
	DefaultTowonelAPIURL = "https://hub.towonel.dev"

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

	// Tunnel DNS handoff (design §6.4).
	CondDNSEndpointSynced = "DNSEndpointSynced"
	ReasonDNSCRDMissing   = "DNSEndpointCRDMissing"
	ReasonNoEdgeTarget    = "NoEdgeTarget"
	ReasonForeignObject   = "ForeignObject"
	ReasonInviteRevoked   = "InviteRevoked"
	// Tunnel region endpoints (#38; design §7). Informational: never gates Ready.
	CondRegionEndpointsAvailable = "RegionEndpointsAvailable"
	// dnsRetryRequeue replaces the 30-minute resync when the DNS step asks to
	// retry (CRD missing, foreign object, API error).
	dnsRetryRequeue = 2 * time.Minute

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
	defaultAgentImage = "codeberg.org/towonel/towonel-agent:1.13.4"
	agentHealthAddr   = "0.0.0.0:9090"
	agentHealthPort   = 9090

	// Connectivity (P6, design §4/§5/§8).
	CondIrohConnectivityReady  = "IrohConnectivityReady"
	ReasonConnectivityReady    = "ConnectivityReady"
	ReasonConnectivitySkipped  = "ConnectivitySkipped"  // invalid combo, non-wedging (design §4)
	ReasonNodeRBACShellMissing = "NodeRBACShellMissing" // chart shell absent (design §5.3)
	ReasonPortIgnored          = "NodePortPortIgnored"  // port set without create (design §4)
	// ReasonEnvIgnored: an --agent-env entry reuses an operator-managed name.
	ReasonEnvIgnored = "EnvIgnored"

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
	// AnnotationAutoRoutes opts a Gateway into auto-tunneling un-annotated child
	// HTTPRoutes (issue #25). By default this is the Gateway's OWN namespace only;
	// AnnotationAutoRoutesNamespaces (below) extends it to listed namespaces (#39).
	// DISTINCT from AnnotationTunnel, which on a Gateway means gateway-as-source
	// (forward the Gateway's own listener hostnames). Both may coexist on one
	// Gateway. Requires AnnotationGatewayService.
	AnnotationAutoRoutes = "towonel.io/auto-routes"
	// AnnotationAutoRoutesNamespaces extends AnnotationAutoRoutes across namespaces
	// (issue #39): a comma-separated namespace allowlist, or the wildcard "all"
	// (any namespace, case-insensitive). Absent → same-namespace only (#25). This
	// is gateway-owner-side consent to PUBLICLY expose those namespaces' un-opted-out
	// routes; per-route towonel.io/tunnel:"false" is the only target-side opt-out.
	AnnotationAutoRoutesNamespaces = "towonel.io/auto-routes-namespaces"
	// AnnotationDNSRecord, present and not truthy on a Service, Gateway or HTTPRoute,
	// keeps every hostname that object contributes out of the tunnel's DNS handoff (it
	// sets AgentService.DNSRecord=false); unrecognized values also warn. The hub still
	// authorizes the hostname.
	AnnotationDNSRecord = "towonel.io/dns-record"

	// Operator-managed agent markers (design §5/§6).
	LabelManagedBy        = "app.kubernetes.io/managed-by"
	ManagedByValue        = "towonel-operator"
	AnnotationAutoCreated = "towonel.io/auto-created"

	// DNSEndpoint ownership (design §6.3): the operator labels and annotation
	// stamped on every emitted DNSEndpoint. LabelOwnerTunnel's value is
	// ownerHash(<ns>, <name>) — stable across tunnel recreation, and the
	// selector of the cleanup sweep and of kubectl orphan cleanup.
	LabelOwnerTunnel         = "towonel.io/owner-tunnel"
	AnnotationOwnerTunnelRef = "towonel.io/owner-tunnel-ref"

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
