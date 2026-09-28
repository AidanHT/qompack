package observer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// BenchmarkOnToolUse_TestOutput256KB_Leased is BenchmarkOnToolUse_TestOutput256KB on the path the
// daemon actually drives. Ingest and drain dispatch every delivery under its leased ObservationID
// after writing its capture sidecar (publication stage 1), so every production capture pays the
// publication's durability passes: the root's objects verified and fsynced, the intent fsynced, the
// index synced, the link written. The unleased benchmark carries no identity and pays none of them,
// so it cannot see that cost at all (SP08-D1).
//
// The sidecars are written before the timer starts, as the daemon writes them before dispatch; what
// is timed is OnToolUse alone, the instrument budget B-C is judged against. The fixtures are the
// unleased benchmark's, byte for byte, and so are the metrics it reports.
func BenchmarkOnToolUse_TestOutput256KB_Leased(b *testing.B) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "toolout", "testrunner", "go-test-rerun.txt"))
	require.NoError(b, err)
	out := repeatTo(string(raw), 256<<10)

	for _, f := range benchFixtures() {
		b.Run(f.name, func(b *testing.B) {
			o, st, metrics := benchHarness(b)
			events := make([]Event, b.N)
			ctxs := make([]context.Context, b.N)
			for i := range events {
				events[i] = bashOf(fmt.Sprintf("toolu_%d", i), "go test ./...", f.vary(out, i))
				arrival := uint64(i + 1)
				id, err := core.NewObservationID(events[i].SessionID, arrival)
				require.NoError(b, err)
				require.NoError(b, store.WriteCaptureSidecar(o.opt.ProjectRoot, store.CaptureSidecar{
					ObservationID: id, Session: events[i].SessionID, Arrival: arrival, Op: opObserveTool,
					Fidelity: core.FidelityExact, Outcome: core.OutcomeOK, Bytes: []byte(events[i].ToolResponse),
				}))
				ctxs[i] = WithObservation(context.Background(), id)
			}
			b.ResetTimer()
			for i := range b.N {
				if _, err := o.OnToolUse(ctxs[i], events[i]); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			reportBudget(b, st, metrics)
		})
	}
}
