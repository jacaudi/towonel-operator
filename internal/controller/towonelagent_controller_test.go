package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// TestAgentReconcileRetryThroughJoinedFailure pins inline-retry behavior for failStatus's joined error.
func TestAgentReconcileRetryThroughJoinedFailure(t *testing.T) {
	agentGR := towonelv1alpha1.GroupVersion.WithResource("towonelagents").GroupResource()
	deployGR := appsv1.SchemeGroupVersion.WithResource("deployments").GroupResource()
	tests := []struct {
		name        string
		applyErr    func(call int) error
		statusErr   func(call int) error
		wantApplies int
		wantErr     func(error) bool
	}{
		{
			name: "plain cause with status conflict retries inline",
			applyErr: func(call int) error {
				if call == 1 {
					return errors.New("transient apply failure")
				}
				return nil
			},
			statusErr: func(call int) error {
				if call == 1 {
					return apierrors.NewConflict(agentGR, "edge", errors.New("object has been modified"))
				}
				return nil
			},
			wantApplies: 2,
			wantErr:     func(err error) bool { return err == nil },
		},
		{
			name:     "API cause with status conflict returns both",
			applyErr: func(int) error { return apierrors.NewForbidden(deployGR, "edge", errors.New("denied")) },
			statusErr: func(int) error {
				return apierrors.NewConflict(agentGR, "edge", errors.New("object has been modified"))
			},
			wantApplies: 1,
			wantErr: func(err error) bool {
				return apierrors.IsForbidden(err) && !apierrors.IsConflict(err) && strings.Contains(err.Error(), "object has been modified")
			},
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
					Services:  []towonelv1alpha1.AgentService{{Hostname: "edge.example", Origin: "edge:80"}},
				},
			}
			store := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(agent, tunnel).
				WithObjects(agent, tunnel, token).Build()
			applies, statusWrites := 0, 0
			cl := interceptor.NewClient(store, interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*appsv1.Deployment); ok {
						applies++
						if err := tc.applyErr(applies); err != nil {
							return err
						}
					}
					return c.Patch(ctx, obj, patch, opts...)
				},
				SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					statusWrites++
					if err := tc.statusErr(statusWrites); err != nil {
						return err
					}
					return c.SubResource(sub).Update(ctx, obj, opts...)
				},
			})
			r := &TowonelAgentReconciler{Client: cl, APIReader: store, Scheme: scheme}

			_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(agent)})

			if !tc.wantErr(err) {
				t.Fatalf("Reconcile error = %v", err)
			}
			if applies != tc.wantApplies {
				t.Fatalf("deployment applies = %d, want %d", applies, tc.wantApplies)
			}
		})
	}
}
