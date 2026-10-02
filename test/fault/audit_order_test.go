package fault

import (
	"context"
	"fmt"
	"os"
	"slices"
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
//
// It observes the order directly (auditRetentionAfterRead names each read as it finishes) and
// through its consequence: a capture is published after whichever read comes first. With the claims
// read first that capture's claim is never seen, so nothing dangles; with the evidence read first
// its claim is seen without its sidecar and dangles. A capture published before the audit keeps the
// retention-roots file present, so both reads run and an earlier claim is resolved against the scan.
func TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence(t *testing.T) {
	sess := core.SessionID("audit-order")

	capture := func(t *testing.T, arrival uint64) store.CaptureSidecar {
		t.Helper()
		id, err := core.NewObservationID(sess, arrival)
		if err != nil {
			t.Fatalf("fault: observation id: %v", err)
		}
		// Distinct bytes per arrival, so each capture declares its own evidence root.
		return store.CaptureSidecar{
			ObservationID: id, Session: sess, Arrival: arrival, Op: "observe.stop",
			Outcome: core.OutcomeOK, Fidelity: core.FidelityExact,
			Bytes: []byte(fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"audit-order","n":%d}`, arrival)),
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
		// A capture already complete when the audit starts: the retention-roots file exists, and its
		// claim must resolve against the sidecar scan.
		if err := store.WriteCaptureSidecar(p.Root, capture(t, 1)); err != nil {
			t.Fatalf("fault: publishing the earlier capture: %v", err)
		}
		// The live daemon's whole publication of one more capture, in its own order, landing after the
		// audit's first read and before its second.
		mid := capture(t, 2)
		var reads []string
		auditRetentionAfterRead = func(read string) {
			reads = append(reads, read)
			if len(reads) != 1 {
				return
			}
			if err := store.WriteCaptureSidecar(p.Root, mid); err != nil {
				t.Errorf("fault: publishing the capture: %v", err)
			}
		}
		t.Cleanup(func() { auditRetentionAfterRead = nil })

		var res auditResult
		auditRetentionRoots(context.Background(), &res, s, p.Root)
		if want := []string{retentionReadClaims, retentionReadEvidence}; !slices.Equal(reads, want) {
			t.Errorf("fault: auditRetentionRoots read %v, want %v: the claims must be read before the "+
				"evidence they name", reads, want)
		}
		if len(res.Dangling) != 0 {
			t.Fatalf("fault: a capture published while the audit ran was reported dangling, though its "+
				"sidecar was on disk before its retention root was declared: %s", describeRefs(res.Dangling))
		}

		// Non-vacuity: the publication did declare both claims, and once it is complete the audit
		// sees both and resolves each against its sidecar.
		auditRetentionAfterRead = nil
		declared, err := os.ReadFile(paths.Long(store.RetentionRootsPath(p.Root)))
		if err != nil {
			t.Fatalf("fault: reading the declared retention roots: %v", err)
		}
		for _, sc := range []store.CaptureSidecar{capture(t, 1), mid} {
			if !strings.Contains(string(declared), string(sc.ObservationID)) {
				t.Fatalf("fault: capture %s declared no evidence retention root: %s", sc.ObservationID, declared)
			}
		}
		var settled auditResult
		auditRetentionRoots(context.Background(), &settled, s, p.Root)
		if len(settled.Dangling) != 0 {
			t.Fatalf("fault: complete captures audit dangling: %s", describeRefs(settled.Dangling))
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
