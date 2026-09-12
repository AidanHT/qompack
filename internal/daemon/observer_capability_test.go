package daemon

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/store"
)

// TestWireObserverStoreSupportsSupersedingRecorder pins, for the REAL composition, that the Store
// the observer is handed can append a tool_use record and the supersede marks it authors as one
// index write.
//
// internal/observer resolves store.SupersedingRecorder once, in New, and falls back to a separate
// RecordToolUse plus one MarkSuperseded per mark when it is absent. That fallback is
// defect-bearing by design: it is the 1+N write shape carried defect SP08-D2's second mechanism
// lives in, where a handler cancelled between the record and its marks leaves a record whose marks
// never landed, and the at-least-once redelivery then appends those marks alone behind a record
// line that predates the flush.
//
// Nothing wraps store.Store today — store.Open returns *FSStore and WireObserver passes it
// straight through — so this assertion holds trivially right now, and that is exactly why it is
// worth making. Every fake in internal/observer's own tests takes the legacy path, so the day a
// composition puts a wrapper between the two (SP20-D1 is explicitly a rewrite of Accept, lease and
// acknowledgement GROUP COMMIT, and an index/ack batching wrapper is the obvious shape for it),
// every observer unit test would stay green and only the e2e x09 flush arm would go red, under
// load, intermittently. This test is what turns that arrival into a named, deterministic failure
// in the package that owns the composition.
//
// It asserts the capability on the value WireObserver actually hands the observer — o.Store after
// wiring — rather than on store.Open's return type directly, because the wiring is the thing that
// can change.
func TestWireObserverStoreSupportsSupersedingRecorder(t *testing.T) {
	root := t.TempDir()
	_, _, o := wireTestDaemon(t, root, nil)

	require.NotNil(t, o.Store, "fixture: WireObserver opened the store")
	_, ok := o.Store.(store.SupersedingRecorder)
	require.True(t, ok,
		"the Store WireObserver hands the observer (%T) does not implement store.SupersedingRecorder, "+
			"so the observer silently falls back to writing a record and its supersede marks as "+
			"separate appends - carried defect SP08-D2's second mechanism, which no unit test in "+
			"internal/observer can see because every fake there takes that same path",
		o.Store)
}
