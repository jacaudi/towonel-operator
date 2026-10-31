package controller

import (
	"fmt"
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

func TestApplyDNSRecordAnnotation(t *testing.T) {
	tests := []struct {
		name        string
		ann         map[string]string
		wantOut     bool // dnsRecord:false stamped on every service entry
		wantWarning bool // InvalidAnnotationValue Event
	}{
		{"absent", nil, false, false},
		{"other annotations only", map[string]string{AnnotationTunnel: "true"}, false, false},
		{"false", map[string]string{AnnotationDNSRecord: "false"}, true, false},
		{"vocabulary no", map[string]string{AnnotationDNSRecord: " No "}, true, false},
		{"vocabulary disabled", map[string]string{AnnotationDNSRecord: "disabled"}, true, false},
		{"true", map[string]string{AnnotationDNSRecord: "true"}, false, false},
		{"unrecognized value fails closed", map[string]string{AnnotationDNSRecord: "flase"}, true, true},
		{"empty value fails closed", map[string]string{AnnotationDNSRecord: ""}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := routing{
				services: []map[string]any{{"hostname": "a.example.com"}, {"hostname": "b.example.com"}},
				tcp:      []map[string]any{{"name": "ssh"}},
			}
			type event struct{ reason, msg string }
			var events []event
			applyDNSRecordAnnotation(rt, tc.ann, func(reason, msg string) { events = append(events, event{reason, msg}) })
			for _, s := range rt.services {
				got, present := s["dnsRecord"]
				if tc.wantOut && got != false {
					t.Errorf("%v: dnsRecord = %v (present=%t), want false", s["hostname"], got, present)
				}
				if !tc.wantOut && present {
					t.Errorf("%v: dnsRecord = %v, want the key omitted", s["hostname"], got)
				}
			}
			if _, present := rt.tcp[0]["dnsRecord"]; present {
				t.Error("tcp entry got dnsRecord; only HTTPS services carry it")
			}
			if !tc.wantWarning {
				if len(events) != 0 {
					t.Errorf("events = %v, want none", events)
				}
				return
			}
			want := event{ReasonInvalidAnnotation, fmt.Sprintf("towonel.io/dns-record %q is not a recognized true/false value; keeping this object's hostnames out of DNS", tc.ann[AnnotationDNSRecord])}
			if len(events) != 1 || events[0] != want {
				t.Errorf("events = %v, want exactly [%v]", events, want)
			}
		})
	}
}
