package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeManaged struct {
	metav1.ObjectMeta
}

func TestRoutingEmpty(t *testing.T) {
	if !(routing{}).empty() {
		t.Fatal("zero routing should be empty")
	}
	if (routing{services: []map[string]any{{"hostname": "x"}}}).empty() {
		t.Fatal("non-empty routing reported empty")
	}
}

func TestOwnsAnyFieldAndSourceManager(t *testing.T) {
	obj := &fakeManaged{}
	obj.SetManagedFields([]metav1.ManagedFieldsEntry{
		{Manager: "towonel-src:Service:ns:a", Operation: metav1.ManagedFieldsOperationApply},
		{Manager: "kubectl", Operation: metav1.ManagedFieldsOperationUpdate},
	})
	if !ownsAnyField(obj, "towonel-src:Service:ns:a") {
		t.Fatal("should own field")
	}
	if ownsAnyField(obj, "towonel-src:Service:ns:z") {
		t.Fatal("should not own field")
	}
	if !hasSourceManager(obj) {
		t.Fatal("should detect a towonel-src manager")
	}
}
