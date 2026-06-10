package controller

import "time"

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

	defaultAgentImage = "codeberg.org/towonel/towonel-agent:latest"
	agentHealthAddr   = "0.0.0.0:9090"
	agentHealthPort   = 9090
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
