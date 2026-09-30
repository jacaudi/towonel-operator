package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// TestAgentReconcileNeverAppliesStaleDeployment mutates an input mid-reconcile and asserts no stale Deployment apply.
func TestAgentReconcileNeverAppliesStaleDeployment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(ctx context.Context, store client.Client) error
		env    string
		stale  string
		fresh  string
	}{
		{
			name: "tunnel allocation changes",
			mutate: func(ctx context.Context, store client.Client) error {
				var tt towonelv1alpha1.TowonelTunnel
				if err := store.Get(ctx, types.NamespacedName{Namespace: "network", Name: "app"}, &tt); err != nil {
					return err
				}
				tt.Status.PortAllocations = []towonelv1alpha1.PortAllocation{{Protocol: "tcp", Name: "ssh", ListenPort: 2222}}
				return store.Status().Update(ctx, &tt)
			},
			env:   "TOWONEL_AGENT_TCP_SERVICES",
			fresh: `"listen_port":2222`,
		},
		{
			name: "agent spec changes",
			mutate: func(ctx context.Context, store client.Client) error {
				var ta towonelv1alpha1.TowonelAgent
				if err := store.Get(ctx, types.NamespacedName{Namespace: "default", Name: "edge"}, &ta); err != nil {
					return err
				}
				ta.Spec.Services = []towonelv1alpha1.AgentService{{Hostname: "new.example", Origin: "new:80"}}
				ta.Generation++ // fake clients do not bump generation on spec writes
				return store.Update(ctx, &ta)
			},
			env:   "TOWONEL_AGENT_SERVICES",
			stale: `"old.example"`,
			fresh: `"new.example"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := agentScheme(t)
			tunnel, token := tunnelWithToken("inv-1", "inv-1", "tok")
			agent := &towonelv1alpha1.TowonelAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "default", UID: "agent-uid", Generation: 1},
				Spec: towonelv1alpha1.TowonelAgentSpec{
					TunnelRef: towonelv1alpha1.TunnelReference{Name: "app", Namespace: "network"},
					Services:  []towonelv1alpha1.AgentService{{Hostname: "old.example", Origin: "old:80"}},
					TCP:       []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "ssh:22"}},
				},
			}
			store := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(agent, tunnel).
				WithObjects(agent, tunnel, token).Build()
			mutated := false
			var applied []string
			cl := interceptor.NewClient(store, interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					switch o := obj.(type) {
					case *corev1.Secret:
						if !mutated {
							mutated = true
							if err := tc.mutate(ctx, store); err != nil {
								return err
							}
						}
					case *appsv1.Deployment:
						v, _ := deploymentEnv(o, tc.env)
						applied = append(applied, v)
					}
					return c.Patch(ctx, obj, patch, opts...)
				},
			})
			r := &TowonelAgentReconciler{Client: cl, APIReader: store, Scheme: scheme}

			if _, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(agent)}); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}

			if !mutated || len(applied) == 0 {
				t.Fatalf("mutated = %t, deployment applies = %d; want the input changed before a deployment apply", mutated, len(applied))
			}
			for i, v := range applied {
				if !strings.Contains(v, tc.fresh) || (tc.stale != "" && strings.Contains(v, tc.stale)) {
					t.Fatalf("deployment apply %d %s = %q; want only the post-change render", i, tc.env, v)
				}
			}
		})
	}
}
