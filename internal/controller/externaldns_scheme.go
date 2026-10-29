package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	externaldnsv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
)

// AddExternalDNSToScheme registers the external-dns DNSEndpoint API
// (externaldns.k8s.io/v1alpha1) — the single registration every scheme that
// touches DNSEndpoints uses (design §6.6). Upstream's AddToScheme registers the
// types only; without metav1.AddToGroupVersion the client cannot convert
// List/Get/Create/Patch options for this group, so those calls fail against a
// real API server (the fake client hides it). external-dns does the same in
// source/crd.go.
func AddExternalDNSToScheme(s *runtime.Scheme) error {
	if err := externaldnsv1alpha1.AddToScheme(s); err != nil {
		return err
	}
	metav1.AddToGroupVersion(s, externaldnsv1alpha1.GroupVersion)
	return nil
}
