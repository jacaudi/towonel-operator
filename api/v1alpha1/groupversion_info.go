// Package v1alpha1 contains the towonel.io/v1alpha1 API types.
// +kubebuilder:object:generate=true
// +groupName=towonel.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// GroupVersion is the group/version for this API.
var GroupVersion = schema.GroupVersion{Group: "towonel.io", Version: "v1alpha1"}

// SchemeBuilder registers the API types into a runtime.Scheme.
var SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

// AddToScheme adds the registered types to a scheme.
var AddToScheme = SchemeBuilder.AddToScheme
