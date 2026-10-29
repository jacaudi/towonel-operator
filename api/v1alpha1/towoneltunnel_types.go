package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// DNSMode selects DNS automation behavior for a tunnel.
// +kubebuilder:validation:Enum=Disabled;DNSEndpoint
type DNSMode string

const (
	DNSModeDisabled    DNSMode = "Disabled"
	DNSModeDNSEndpoint DNSMode = "DNSEndpoint"
)

// DeletionPolicy controls invite teardown when a tunnel is deleted.
// +kubebuilder:validation:Enum=Delete;Retain
type DeletionPolicy string

const (
	DeletionPolicyDelete DeletionPolicy = "Delete"
	DeletionPolicyRetain DeletionPolicy = "Retain"
)

// DNSProviderProperty is one provider-specific setting copied onto every emitted
// endpoint (external-dns ProviderSpecificProperty), e.g.
// {name: "cloudflare/proxied", value: "false"}.
type DNSProviderProperty struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:MaxLength=4096
	// +optional
	Value string `json:"value,omitempty"`
}

// TunnelDNSSpec configures vendor-neutral DNS automation (emits an external-dns DNSEndpoint).
type TunnelDNSSpec struct {
	// +kubebuilder:default=Disabled
	// +optional
	Mode DNSMode `json:"mode,omitempty"`
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=1
	// +optional
	DefaultTTL int32 `json:"defaultTTL,omitempty"`
	// Namespace for the emitted DNSEndpoint; defaults to the tunnel's namespace.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// CNAMETarget overrides the operator's region→edge-name map for this tunnel.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\.)+[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\.?$`
	// +optional
	CNAMETarget string `json:"cnameTarget,omitempty"`
	// IncludeWildcards emits records for wildcard hostnames (*.example.com). Default false.
	// +optional
	IncludeWildcards bool `json:"includeWildcards,omitempty"`
	// Labels are copied onto the emitted DNSEndpoint (pair with external-dns --label-filter).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations are copied onto the emitted DNSEndpoint (pair with --annotation-filter).
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
	// ProviderSpecific entries are copied onto every emitted endpoint.
	// +kubebuilder:validation:MaxItems=100
	// +optional
	ProviderSpecific []DNSProviderProperty `json:"providerSpecific,omitempty"`
}

// TowonelTunnelSpec defines the desired tunnel (invite + authorization).
type TowonelTunnelSpec struct {
	// +kubebuilder:default=EU
	// +kubebuilder:validation:Enum=EU;CA
	// +optional
	Region string `json:"region,omitempty"`
	// +kubebuilder:validation:items:Enum=EU;CA
	// +optional
	FailoverRegions []string `json:"failoverRegions,omitempty"`
	// +optional
	APIKeySecretRef *SecretKeyRef `json:"apiKeySecretRef,omitempty"`
	// TokenExpiry maps to invite expires_in_secs; 0 = never (hub caps finite at 30d).
	// +kubebuilder:default=0
	// +optional
	TokenExpiry int64 `json:"tokenExpiry,omitempty"`
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
	// ExtraHostnames authorizes hostnames no agent serves yet (escape hatch).
	// +optional
	ExtraHostnames []string `json:"extraHostnames,omitempty"`
	// +optional
	DNS TunnelDNSSpec `json:"dns,omitempty"`
}

// EdgeRef identifies an edge node and its public addresses.
type EdgeRef struct {
	// +optional
	NodeID string `json:"nodeId,omitempty"`
	// +optional
	Addresses []string `json:"addresses,omitempty"`
}

// PortAllocation is a reserved public TCP/UDP port for one agent l4 service.
type PortAllocation struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Enum=tcp;udp
	Protocol   string `json:"protocol"`
	ListenPort int32  `json:"listenPort"`
	// +optional
	Edge EdgeRef `json:"edge,omitempty"`
}

// DNSRecord is computed DNS intent (observability; not an integration contract), populated whenever an edge target resolves, in both DNS modes.
type DNSRecord struct {
	Hostname string `json:"hostname"`
	// +kubebuilder:validation:Enum=A;AAAA;CNAME
	Type   string `json:"type"`
	Target string `json:"target"`
	// +optional
	Port int32 `json:"port,omitempty"`
	// +optional
	Protocol string `json:"protocol,omitempty"`
}

// RegionEndpoint is the live serving state for one configured region (#38).
type RegionEndpoint struct {
	// Region is the configured region code, e.g. "EU" or "CA".
	Region string `json:"region"`
	// Role is "primary" or "secondary" (the hub's "failover" is mapped);
	// unknown hub roles pass through verbatim.
	Role string `json:"role"`
	// Addresses is the sorted, deduplicated union of live edge public IPs
	// serving this region. Empty means configured but no live edge right now.
	// +optional
	Addresses []string `json:"addresses,omitempty"`
}

// TowonelTunnelStatus is the observed tunnel state.
type TowonelTunnelStatus struct {
	// +optional
	InviteID string `json:"inviteId,omitempty"`
	// +optional
	TenantID string `json:"tenantId,omitempty"`
	// +optional
	TokenSecretRef *SecretReference `json:"tokenSecretRef,omitempty"`
	// ExpiresAt in epoch ms; 0 = never.
	// +optional
	ExpiresAt int64 `json:"expiresAt,omitempty"`
	// +optional
	AuthorizedHostnames []string `json:"authorizedHostnames,omitempty"`
	// +optional
	PortAllocations []PortAllocation `json:"portAllocations,omitempty"`
	// +optional
	Edges []string `json:"edges,omitempty"`
	// +optional
	DNSRecords []DNSRecord `json:"dnsRecords,omitempty"`
	// RegionEndpoints is the live serving state per configured region, from the
	// hub's GET /v1/invites/{id}/status (#38): primary first, then failover.
	// +listType=atomic
	// +optional
	RegionEndpoints []RegionEndpoint `json:"regionEndpoints,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// Phase is a coarse, human-readable rollup of the Ready condition.
	// +kubebuilder:validation:Enum=Pending;Ready;Error
	// +optional
	Phase string `json:"phase,omitempty"`
	// ObservedGeneration is the generation most recently reconciled into status.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=twt
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="InviteID",type=string,JSONPath=`.status.inviteId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TowonelTunnel is the Schema for the towoneltunnels API.
type TowonelTunnel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec TowonelTunnelSpec `json:"spec,omitempty"`
	// +optional
	Status TowonelTunnelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TowonelTunnelList contains a list of TowonelTunnel.
type TowonelTunnelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TowonelTunnel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TowonelTunnel{}, &TowonelTunnelList{})
}
