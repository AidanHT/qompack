package fault

import (
	"os"
	"testing"

	"github.com/qompack/qompack/internal/paths"
)

// The audit's own test. Everything else in this package asserts over auditProject's answers, so an
// audit that cannot see a missing object makes every one of those rows vacuous — and that is not
// hypothetical: round 1 shipped exactly that defect, and the row that removed an object file
// recorded `dangling 0 -> 0`.
//
// It is a composition-root test over the real product: a real session through the installed binary,
// then one object file deleted from under a live index, then the audit asked what it sees.

// TestFault_AuditSeesADeletedObjectUnderALiveIndex is the non-vacuity check for every other row.
func TestFault_AuditSeesADeletedObjectUnderALiveIndex(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	sess := sessionID("audit-selftest")
	seedSession(t, b, p, sess)
	shutdownIfReachable(t, p.Root)

	before := auditProject(t, p.Root)
	if len(before.Dangling) != 0 {
		t.Fatalf("fault: a freshly seeded project must audit clean, or this test cannot attribute "+
			"what it finds afterwards; got %s", describeRefs(before.Dangling))
	}

	target, ok := firstIndexedToolUse(p.Root)
	if !ok {
		t.Fatalf("fault: the seeded session left no tool_use record to point at")
	}
	chunk, ok := firstChunkOf(t, p.Root, target.Root)
	if !ok {
		t.Fatalf("fault: the seeded root %s names no chunk on disk", shortHash(target.Root))
	}
	path, ok := objectFilePath(p.Root, chunk)
	if !ok {
		t.Fatalf("fault: no object file backs chunk %s", shortHash(chunk.String()))
	}
	if err := os.Remove(paths.Long(path)); err != nil {
		t.Fatalf("fault: removing %s: %v", path, err)
	}

	// The index is untouched, so store.Has still answers "present" for that chunk: FSStore.Has
	// reads an in-memory chunkSet built from index/roots.jsonl at Open and never stats a file. An
	// audit that asks Has sees nothing here, which is precisely the blindness this test exists to
	// keep out.
	after := auditProject(t, p.Root)
	newRefs := newlyDangling(before, after)
	if len(newRefs) == 0 {
		t.Fatalf("fault: the audit did not notice that the object backing chunk %s was deleted while "+
			"its index line survived. Every other row in this package resolves references through "+
			"the same code, so an audit blind to this reports a clean store for a store that has "+
			"lost its bytes.\naudit before %s\naudit after  %s",
			shortHash(chunk.String()), before, after)
	}
	t.Logf("fault: deleting one object under a live index produced %d dangling reference(s): %s",
		len(newRefs), describeRefs(newRefs))
}
