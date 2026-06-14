package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TunnelReference points at the TowonelTunnel this agent connects to.
type TunnelReference struct {
	Name string `json:"name"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// AgentService is an HTTPS/SNI route (hostname -> origin).
type AgentService struct {
	Hostname string `json:"hostname"`
	Origin   string `json:"origin"`
	// EdgeTLSMode selects edge TLS behavior: `passthrough` (default; edge peeks SNI,
	// forwards raw TLS, origin terminates) | `terminate` (edge issues on-demand cert,
	// forwards plaintext). The agent never terminates TLS.
	// +kubebuilder:default=passthrough
	// +optional
	EdgeTLSMode string `json:"edgeTLSMode,omitempty"`
	// +optional
	ProxyProtocol bool `json:"proxyProtocol,omitempty"`
}

// AgentL4Service is a raw TCP/UDP service (origin host:port).
type AgentL4Service struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
	// PreferredPort pins the public listen port (honored if free).
	// +optional
	PreferredPort int32 `json:"preferredPort,omitempty"`
	// Hostname, if set, yields an A record to the edge (port informational).
	// +optional
	Hostname string `json:"hostname,omitempty"`
}

// NodePortSpec controls an optional UDP NodePort Service for iroh direct-path.
type NodePortSpec struct {
	// Create makes the operator create+own a UDP NodePort Service for the agent.
	// +optional
	Create bool `json:"create,omitempty"`
	// Name overrides the created Service's name (default: "<agent>-iroh").
	// +optional
	Name string `json:"name,omitempty"`
	// Port pins the external node port for stable firewall/NAT-forward rules.
	// Omitted (0) -> Kubernetes auto-assigns; the agent reads whatever port exists.
	// Ignored when Create is false.
	// +optional
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	Port int32 `json:"port,omitempty"`
}

// ConnectivitySpec is the optional direct-path (NAT-traversal) optimization.
type ConnectivitySpec struct {
	// +optional
	Autodiscover bool `json:"autodiscover,omitempty"`
	// IrohPort pins the agent's bound UDP port (TOWONEL_AGENT_IROH_PORT).
	// Required when nodePort.create is true.
	// +optional
	// +kubebuilder:validation:Maximum=65535
	IrohPort int32 `json:"irohPort,omitempty"`
	// +optional
	ExtraLocalAddrs []string `json:"extraLocalAddrs,omitempty"`
	// +optional
	NodePort NodePortSpec `json:"nodePort,omitempty"`
}

// WorkloadSpec are the agent connector knobs.
type WorkloadSpec struct {
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`
	// +optional
	Image string `json:"image,omitempty"`
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// TowonelAgentSpec defines the desired agent (routing + workload).
type TowonelAgentSpec struct {
	TunnelRef TunnelReference `json:"tunnelRef"`
	// +listType=map
	// +listMapKey=hostname
	// +optional
	Services []AgentService `json:"services,omitempty"`
	// +listType=map
	// +listMapKey=name
	// +optional
	TCP []AgentL4Service `json:"tcp,omitempty"`
	// +listType=map
	// +listMapKey=name
	// +optional
	UDP []AgentL4Service `json:"udp,omitempty"`
	// +optional
	RelayURL string `json:"relayUrl,omitempty"`
	// +optional
	Connectivity ConnectivitySpec `json:"connectivity,omitempty"`
	// +optional
	Workload WorkloadSpec `json:"workload,omitempty"`
}

// TowonelAgentStatus is the observed agent state.
type TowonelAgentStatus struct {
	// +kubebuilder:validation:Enum=Pending;WaitingForTunnel;Ready
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	ObservedConfigHash string `json:"observedConfigHash,omitempty"`
	// +optional
	RenderedSecretRef *SecretReference `json:"renderedSecretRef,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the generation most recently reconciled into status.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=twa
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tunnel",type=string,JSONPath=`.spec.tunnelRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TowonelAgent is the Schema for the towonelagents API.
type TowonelAgent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec TowonelAgentSpec `json:"spec,omitempty"`
	// +optional
	Status TowonelAgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TowonelAgentList contains a list of TowonelAgent.
type TowonelAgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TowonelAgent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TowonelAgent{}, &TowonelAgentList{})
}
