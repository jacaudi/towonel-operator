package controller

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// agentTLSMode is the agent's nested tls_mode wire object, e.g. {"mode":"passthrough"}.
// The Towonel agent (>=0.1.32) deserializes tls_mode as a struct, NOT a bare string —
// emitting "tls_mode":"passthrough" fails with `invalid type: string, expected struct Wire`.
type agentTLSMode struct {
	Mode string `json:"mode"`
}

// Towonel agent env contract (snake_case JSON). Local config, deliberately
// NOT part of the P1 API client (design §4.E).
type agentHTTPSService struct {
	Hostname      string        `json:"hostname"`
	Origin        string        `json:"origin"`
	TLSMode       *agentTLSMode `json:"tls_mode,omitempty"`
	ProxyProtocol string        `json:"proxy_protocol,omitempty"`
}

type agentL4JSON struct {
	Name       string `json:"name"`
	ListenPort int32  `json:"listen_port"`
	Origin     string `json:"origin"`
}

// agentConfig is the fully-rendered env payload for one agent.
type agentConfig struct {
	ServicesJSON string
	TCPJSON      string
	UDPJSON      string
	RelayURL     string
	InviteID     string // token identity stand-in: rotation rolls the hash (§4.F)
	Image        string
	Pending      []string // "proto/name" entries awaiting allocation (§4.E)
	// connectivity (P6, design §7)
	SAName      string          // pod serviceAccountName (always set)
	IrohPort    int32           // UDP containerPort when >0
	ConnEnv     []corev1.EnvVar // connectivity env vars, appended to the container
	ConnEnvHash string          // deterministic digest of ConnEnv for hash()
}

// hash is the single rollout trigger (design §4.F). The token VALUE is never
// hashed — InviteID stands in for it.
func (c agentConfig) hash() string {
	h := sha256.New()
	for _, s := range []string{c.Image, c.InviteID, c.ServicesJSON, c.TCPJSON, c.UDPJSON, c.RelayURL, c.SAName, c.ConnEnvHash} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func marshalL4(entries []towonelv1alpha1.AgentL4Service, protocol string, allocs map[string]int32, pending *[]string) (string, error) {
	out := make([]agentL4JSON, 0, len(entries))
	for _, e := range entries {
		port, ok := allocs[allocKey(protocol, e.Name)]
		if !ok { // partial render: omit + report (design §4.E)
			*pending = append(*pending, allocKey(protocol, e.Name))
			continue
		}
		out = append(out, agentL4JSON{Name: e.Name, ListenPort: port, Origin: e.Origin})
	}
	if len(out) == 0 {
		return "", nil // empty -> env var omitted entirely
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshal %s services: %w", protocol, err)
	}
	return string(b), nil
}

// renderConfig renders the agent env from spec + the tunnel's allocations.
func renderConfig(ta *towonelv1alpha1.TowonelAgent, allocations []towonelv1alpha1.PortAllocation, inviteID string) (agentConfig, error) {
	cfg := agentConfig{
		RelayURL: ta.Spec.RelayURL,
		InviteID: inviteID,
		Image:    cmp.Or(ta.Spec.Workload.Image, defaultAgentImage),
	}
	if len(ta.Spec.Services) > 0 {
		svcs := make([]agentHTTPSService, 0, len(ta.Spec.Services))
		for _, s := range ta.Spec.Services {
			entry := agentHTTPSService{Hostname: s.Hostname, Origin: s.Origin, ProxyProtocol: s.ProxyProtocol}
			if s.EdgeTLSMode != "" {
				entry.TLSMode = &agentTLSMode{Mode: s.EdgeTLSMode}
			}
			svcs = append(svcs, entry)
		}
		b, err := json.Marshal(svcs)
		if err != nil {
			return cfg, fmt.Errorf("marshal services: %w", err)
		}
		cfg.ServicesJSON = string(b)
	}
	ports := map[string]int32{}
	for _, pa := range allocations {
		ports[allocKey(pa.Protocol, pa.Name)] = pa.ListenPort
	}
	var err error
	if cfg.TCPJSON, err = marshalL4(ta.Spec.TCP, "tcp", ports, &cfg.Pending); err != nil {
		return cfg, err
	}
	if cfg.UDPJSON, err = marshalL4(ta.Spec.UDP, "udp", ports, &cfg.Pending); err != nil {
		return cfg, err
	}
	// connectivity (P6) — orthogonal to routing; folded into the rollout hash.
	cfg.SAName = agentSAName(ta.Name)
	p := planConnectivity(ta)
	cfg.IrohPort = p.irohPort
	cfg.ConnEnv = connectivityEnv(ta, p)
	if len(cfg.ConnEnv) > 0 {
		b, err := json.Marshal(cfg.ConnEnv) // deterministic: fixed field + slice order
		if err != nil {
			return cfg, fmt.Errorf("marshal connectivity env: %w", err)
		}
		cfg.ConnEnvHash = string(b)
	}
	return cfg, nil
}

func agentEnv(ta *towonelv1alpha1.TowonelAgent, cfg agentConfig) []corev1.EnvVar {
	env := []corev1.EnvVar{{
		Name: "TOWONEL_INVITE_TOKEN",
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: tokenSecretName(ta.Name)},
			Key:                  tokenDataKey,
		}},
	}}
	add := func(name, value string) {
		if value != "" {
			env = append(env, corev1.EnvVar{Name: name, Value: value})
		}
	}
	add("TOWONEL_AGENT_SERVICES", cfg.ServicesJSON)
	add("TOWONEL_AGENT_TCP_SERVICES", cfg.TCPJSON)
	add("TOWONEL_AGENT_UDP_SERVICES", cfg.UDPJSON)
	add("TOWONEL_AGENT_RELAY_URL", cfg.RelayURL)
	env = append(env, cfg.ConnEnv...)
	env = append(env, corev1.EnvVar{Name: "TOWONEL_AGENT_HEALTH_LISTEN_ADDR", Value: agentHealthAddr})
	return env
}

// agentPodSecurityContext returns the user-set pod securityContext or, when
// unset, the upstream least-priv default (towonel.dev/docs/agent/kubernetes).
func agentPodSecurityContext(ta *towonelv1alpha1.TowonelAgent) *corev1.PodSecurityContext {
	if ta.Spec.Workload.PodSecurityContext != nil {
		return ta.Spec.Workload.PodSecurityContext
	}
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   ptr.To(true),
		RunAsUser:      ptr.To(int64(10001)),
		RunAsGroup:     ptr.To(int64(10001)),
		FSGroup:        ptr.To(int64(10001)),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

// agentContainerSecurityContext returns the user-set container securityContext
// or, when unset, the upstream least-priv default. readOnlyRootFilesystem is
// safe: the agent reads config from env only and writes no files.
func agentContainerSecurityContext(ta *towonelv1alpha1.TowonelAgent) *corev1.SecurityContext {
	if ta.Spec.Workload.SecurityContext != nil {
		return ta.Spec.Workload.SecurityContext
	}
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// buildDeployment renders the agent workload (parent §5.2 defaults).
func buildDeployment(ta *towonelv1alpha1.TowonelAgent, cfg agentConfig) *appsv1.Deployment {
	labels := map[string]string{
		LabelAppName:     AgentAppName,
		LabelAppInstance: ta.Name,
		LabelPartOf:      PartOfValue,
	}
	selector := map[string]string{LabelAppName: AgentAppName, LabelAppInstance: ta.Name}
	replicas := ta.Spec.Workload.Replicas
	if replicas == nil {
		replicas = new(int32(1))
	}
	res := *ta.Spec.Workload.Resources.DeepCopy()
	if res.Requests == nil {
		res.Requests = corev1.ResourceList{}
	}
	if _, ok := res.Requests[corev1.ResourceMemory]; !ok { // OOM-safe floor (CF-op lesson)
		res.Requests[corev1.ResourceMemory] = resource.MustParse("128Mi")
	}
	if res.Limits == nil {
		res.Limits = corev1.ResourceList{}
	}
	if _, ok := res.Limits[corev1.ResourceMemory]; !ok { // OOM-safe ceiling
		res.Limits[corev1.ResourceMemory] = resource.MustParse("512Mi")
	}
	// Liveness gates on /healthz: is the agent process up? A failure restarts the
	// container. Readiness gates on /readyz, which returns 503 until the agent has
	// an active edge session (towonel-agent v1.0.0) — a truer "ready to serve
	// traffic" signal (issue #42). Because Endpoints/rollout gate on readiness, an
	// agent with no live session reports NotReady (intended; keep liveness on
	// /healthz so a session-less-but-healthy agent is NOT restart-looped).
	livenessProbe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(agentHealthPort)},
	}}
	readinessProbe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt32(agentHealthPort)},
	}}
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name: ta.Name, Namespace: ta.Namespace, Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{AnnotationConfigHash: cfg.hash()},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: cfg.SAName,
					SecurityContext:    agentPodSecurityContext(ta),
					NodeSelector:       ta.Spec.Workload.NodeSelector,
					Tolerations:        ta.Spec.Workload.Tolerations,
					Containers: []corev1.Container{{
						Name:            AgentAppName,
						Image:           cfg.Image,
						SecurityContext: agentContainerSecurityContext(ta),
						Env:             agentEnv(ta, cfg),
						Ports:           agentContainerPorts(cfg.IrohPort),
						Resources:       res,
						LivenessProbe:   livenessProbe,
						ReadinessProbe:  readinessProbe,
					}},
				},
			},
		},
	}
}

// deploymentNeedsWrite gates the SSA apply (design §4.F write suppression):
// hash covers env+image; replicas/resources/scheduling sit outside it.
func deploymentNeedsWrite(current, desired *appsv1.Deployment) bool {
	if len(current.Spec.Template.Spec.Containers) == 0 {
		return true
	}
	if current.Spec.Template.Annotations[AnnotationConfigHash] != desired.Spec.Template.Annotations[AnnotationConfigHash] {
		return true
	}
	return !equality.Semantic.DeepEqual(current.Spec.Replicas, desired.Spec.Replicas) ||
		!equality.Semantic.DeepEqual(current.Spec.Template.Spec.Containers[0].Resources, desired.Spec.Template.Spec.Containers[0].Resources) ||
		!equality.Semantic.DeepEqual(current.Spec.Template.Spec.NodeSelector, desired.Spec.Template.Spec.NodeSelector) ||
		!equality.Semantic.DeepEqual(current.Spec.Template.Spec.Tolerations, desired.Spec.Template.Spec.Tolerations) ||
		!equality.Semantic.DeepEqual(current.Spec.Template.Spec.SecurityContext, desired.Spec.Template.Spec.SecurityContext) ||
		!equality.Semantic.DeepEqual(current.Spec.Template.Spec.Containers[0].SecurityContext, desired.Spec.Template.Spec.Containers[0].SecurityContext)
}

// ensureDeployment applies the rendered Deployment and returns the live
// object (for WorkloadAvailable projection).
func (r *TowonelAgentReconciler) ensureDeployment(ctx context.Context, ta *towonelv1alpha1.TowonelAgent, cfg agentConfig) (*appsv1.Deployment, error) {
	desired := buildDeployment(ta, cfg)
	if err := controllerutil.SetControllerReference(ta, desired, r.Scheme); err != nil {
		return nil, fmt.Errorf("set owner ref: %w", err)
	}
	nn := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	var current appsv1.Deployment
	getErr := r.Get(ctx, nn, &current)
	if getErr == nil && !deploymentNeedsWrite(&current, desired) {
		return &current, nil
	}
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return nil, fmt.Errorf("get deployment %s: %w", nn, getErr)
	}
	if err := r.Patch(ctx, desired, client.Apply, client.FieldOwner(FieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("apply deployment %s: %w", nn, err)
	}
	if err := r.Get(ctx, nn, &current); err != nil {
		return desired, nil // freshly applied; status will settle next pass
	}
	return &current, nil
}
