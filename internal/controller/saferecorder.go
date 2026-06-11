package controller

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SafeRecorder wraps a record.EventRecorder with nil-safety. A typed-nil
// *SafeRecorder and one wrapping a nil recorder are both no-ops.
type SafeRecorder struct{ record.EventRecorder }

func NewSafeRecorder(rec record.EventRecorder) *SafeRecorder {
	return &SafeRecorder{EventRecorder: rec}
}

func (s *SafeRecorder) Event(o runtime.Object, etype, reason, msg string) {
	if s == nil || s.EventRecorder == nil {
		return
	}
	s.EventRecorder.Event(o, etype, reason, msg)
}

func (s *SafeRecorder) Eventf(o runtime.Object, etype, reason, f string, a ...any) {
	if s == nil || s.EventRecorder == nil {
		return
	}
	s.EventRecorder.Eventf(o, etype, reason, f, a...)
}

const (
	eventDedupeTTL     = time.Hour
	eventDedupeMaxSize = 1024
)

// eventDedupe collapses identical (uid, reason, message) emits to one per TTL.
type eventDedupe struct {
	mu     sync.Mutex
	recent map[string]time.Time
}

func newEventDedupe() *eventDedupe { return &eventDedupe{recent: map[string]time.Time{}} }

func (d *eventDedupe) emit(rec *SafeRecorder, target client.Object, etype, reason, msg string) {
	if rec == nil {
		return
	}
	k := string(target.GetUID()) + "|" + reason + "|" + msg
	d.mu.Lock()
	now := time.Now()
	if t, ok := d.recent[k]; ok && now.Sub(t) < eventDedupeTTL {
		d.mu.Unlock()
		return
	}
	if len(d.recent) >= eventDedupeMaxSize {
		var oldKey string
		var oldTime time.Time
		first := true
		for ek, et := range d.recent {
			if first || et.Before(oldTime) {
				oldKey, oldTime, first = ek, et, false
			}
		}
		delete(d.recent, oldKey)
	}
	d.recent[k] = now
	d.mu.Unlock()
	rec.Event(target, etype, reason, msg)
}
