package v1alpha1

// SecretKeyRef references a single key within a Secret in the same namespace.
type SecretKeyRef struct {
	// Name of the Secret.
	Name string `json:"name"`
	// Key within the Secret holding the value.
	Key string `json:"key"`
}

// SecretReference names a Secret, optionally in another namespace.
type SecretReference struct {
	Name string `json:"name"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
}
