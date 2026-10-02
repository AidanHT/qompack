package fault

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The audit beside a LIVE daemon. TestFault_Lifecycle audits its one shared daemon's store between
// rows without stopping it, and the row that ends on a delivery nothing waits for —
// out_of_order_sessionend, whose Stop arrives after SessionEnd — audits while the daemon is still
// publishing that Stop. A capture is published in a fixed order (internal/store/capture_sidecar.go,
// writeCaptureSidecar): the sidecar is renamed into records/captures/ first, and only then is its
// evidence-class retention root appended. An audit that read the sidecars BEFORE the retention roots
// could see the claim without the evidence it names, and call a capture that was never anything but
// complete a dangling reference: run 36955046276's windows-latest test job did exactly that on its
// second -count pass ("retention root (class \"evidence\", ...) names 158e3206c3f5, which is not
// held", sidecars 56->57 while the Stop's own sidecar was still being written), and the
// frontier_state_is_explicit row's full audit with the daemon stopped found the store clean.

// TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence pins the read order: the claim first,
// then the evidence, so every claim the audit sees was published after its sidecar was on disk.
func TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence(t *testing.T) {
	sess := core.SessionID("audit-order")

	capture := func(t *testing.T, arrival uint64) store.CaptureSidecar {
		t.Helper()
		id, err := core.NewObservationID(sess, arrival)
		if err != nil {
			t.Fatalf("fault: observation id: %v", err)
		}
		return store.CaptureSidecar{
			ObservationID: id, Session: sess, Arrival: arrival, Op: "observe.stop",
			Outcome: core.OutcomeOK, Fidelity: core.FidelityExact,
			Bytes: []byte(`{"hook_event_name":"Stop","session_id":"audit-order"}`),
		}
	}
	openStore := func(t *testing.T, root string) store.Store {
		t.Helper()
		s, err := store.Open(root, config.Defaults(), store.Deps{})
		if err != nil {
			t.Fatalf("fault: store.Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}

	t.Run("a_capture_published_mid_audit_is_not_dangling", func(t *testing.T) {
		p := newProject(t, "proj")
		s := openStore(t, p.Root)
		sc := capture(t, 1)
		// The live daemon's whole publication of one capture, in its own order, landing between the
		// audit's two reads.
		auditRetentionBetweenReads = func() {
			if err := store.WriteCaptureSidecar(p.Root, sc); err != nil {
				t.Errorf("fault: publishing the capture: %v", err)
			}
		}
		t.Cleanup(func() { auditRetentionBetweenReads = nil })

		var res auditResult
		auditRetentionRoots(context.Background(), &res, s, p.Root)
		if len(res.Dangling) != 0 {
			t.Fatalf("fault: a capture published while the audit ran was reported dangling, though its "+
				"sidecar was on disk before its retention root was declared: %s", describeRefs(res.Dangling))
		}

		// Non-vacuity: the publication did declare the claim, and once it is complete the audit sees
		// the claim and resolves it against the sidecar.
		auditRetentionBetweenReads = nil
		declared, err := os.ReadFile(paths.Long(store.RetentionRootsPath(p.Root)))
		if err != nil || !strings.Contains(string(declared), string(store.RetentionEvidence)) {
			t.Fatalf("fault: the capture declared no evidence retention root (err %v): %s", err, declared)
		}
		var settled auditResult
		auditRetentionRoots(context.Background(), &settled, s, p.Root)
		if len(settled.Dangling) != 0 {
			t.Fatalf("fault: a complete capture audits dangling: %s", describeRefs(settled.Dangling))
		}
	})

	t.Run("an_evidence_claim_with_no_sidecar_is_still_dangling", func(t *testing.T) {
		p := newProject(t, "proj")
		s := openStore(t, p.Root)
		orphan := core.HashBytes("qompack.capture.v1", []byte("a capture whose sidecar was never written"))
		if err := store.AppendRetentionRoot(p.Root, store.RetentionRoot{
			Hash: orphan, Class: store.RetentionEvidence, Reason: "capture sidecar never written",
		}); err != nil {
			t.Fatalf("fault: declaring the claim: %v", err)
		}

		var res auditResult
		auditRetentionRoots(context.Background(), &res, s, p.Root)
		if len(res.Dangling) != 1 || res.Dangling[0].Kind != refRetentionRoot {
			t.Fatalf("fault: an evidence claim no sidecar backs must stay one dangling retention root, got %s",
				describeRefs(res.Dangling))
		}
	})
}
