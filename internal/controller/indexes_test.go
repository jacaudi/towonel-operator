package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func TestResolvedTunnelRef(t *testing.T) {
	tests := []struct {
		name  string
		agent towonelv1alpha1.TowonelAgent
		want  string
	}{
		{
			name: "explicit namespace",
			agent: towonelv1alpha1.TowonelAgent{
				ObjectMeta: metav1.ObjectMeta{Namespace: "selfhosted"},
				Spec: towonelv1alpha1.TowonelAgentSpec{
					TunnelRef: towonelv1alpha1.TunnelReference{Name: "app", Namespace: "network"},
				},
			},
			want: "network/app",
		},
		{
			name: "defaults to agent namespace",
			agent: towonelv1alpha1.TowonelAgent{
				ObjectMeta: metav1.ObjectMeta{Namespace: "selfhosted"},
				Spec: towonelv1alpha1.TowonelAgentSpec{
					TunnelRef: towonelv1alpha1.TunnelReference{Name: "app"},
				},
			},
			want: "selfhosted/app",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvedTunnelRef(&tt.agent).String(); got != tt.want {
				t.Errorf("resolvedTunnelRef = %q, want %q", got, tt.want)
			}
		})
	}
}
