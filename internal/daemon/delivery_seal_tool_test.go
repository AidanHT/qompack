package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// toolTestProject builds a project whose two seals are v2 and whose daemon is gone, holding n
// leases. It returns the project root.
//
// v2 on disk with no daemon running is what a CRASH leaves: a clean Release downgrades both seals
// to v1 (closeSeals), and a journal carrying a fault is the state in which it does not. Poisoning
// the handle reproduces exactly that without killing a process, and it is the state the offline
// tool exists for — design §4.4's "step 2 to pre-step-1, after a crash or a failed Release".
//
// n is at most six: testDeliveryToken repeats one rune, and a delivery token must be lowercase hex.
func toolTestProject(t *testing.T, n int) string {
	t.Helper()

	root := t.TempDir()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = 2
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	for i := range n {
		delivery := testDeliveryToken(rune('a' + i))
		_, err := j.lease(context.Background(), delivery, "s1", testDeliveryRequest(delivery))
		require.NoError(t, err)
	}
	_ = j.poison(deliveryJournalError())
	require.NoError(t, lock.Release())
	return root
}

// toolTestState is every file the tool could touch, as bytes. A refusal must leave all four equal.
func toolTestState(t *testing.T, root string) map[string][]byte {
	t.Helper()

	state := paths.Of(root).State
	files := map[string][]byte{}
	for _, name := range []string{
		deliveryLeaseFile, deliveryPositionFile, deliveryAckFile, deliveryAckPositionFile,
	} {
		b, err := os.ReadFile(filepath.Join(state, name))
		require.NoError(t, err)
		files[name] = b
	}
	return files
}

// toolTestSeals describes both seal files: what a refusal was looking at.
func toolTestSeals(t *testing.T, root string) string {
	t.Helper()

	const head = 48
	state := paths.Of(root).State
	var b bytes.Buffer
	for _, name := range []string{deliveryPositionFile, deliveryAckPositionFile} {
		raw, err := os.ReadFile(filepath.Join(state, name))
		require.NoError(t, err)
		if len(raw) > head {
			raw = raw[:head]
		}
		fmt.Fprintf(&b, "\n  %s: %q", name, raw)
	}
	return b.String()
}

// toolTestRun runs the tool against root and returns its report.
func toolTestRun(t *testing.T, root string, o DeliverySealOptions) (string, error) {
	t.Helper()

	var out bytes.Buffer
	o.ProjectRoot, o.Out, o.Clock = root, &out, newFakeClock(epoch)
	// The call first, then the report: a single return statement would evaluate out.String() before
	// RepairDeliverySeal ran and hand every assertion an empty string.
	err := RepairDeliverySeal(o)
	t.Logf("report:\n%s", out.String())
	return out.String(), err
}

// tearNewestSlot makes the seal's effective slot unreadable and leaves the older one valid: the
// valid-plus-invalid image the strict reader refuses (design §2.9) and the only one Rule R accepts.
func tearNewestSlot(t *testing.T, root string) {
	t.Helper()

	p := filepath.Join(paths.Of(root).State, deliveryPositionFile)
	image, err := os.ReadFile(p)
	require.NoError(t, err)
	require.True(t, isDeliverySealImage(image), "the crash should have left a v2 image")

	effective, _, err := selectSeal(image, deliveryChainDomain, deliveryChainSeed)
	require.NoError(t, err)
	// One byte inside the record's first key: still a v2 image, still padded to the region's end,
	// no longer the canonical JSON of a record, so the slot is invalid rather than empty.
	const inFirstKey = 3
	image[slotFor(effective.Seq).offset()+inFirstKey] = 'X'
	require.NoError(t, os.WriteFile(p, image, 0o600))

	_, _, err = selectSeal(image, deliveryChainDomain, deliveryChainSeed)
	require.Error(t, err, "the strict reader must refuse a torn slot; otherwise this fixture tests nothing")
}

// requireOpensWithFormat asserts that a daemon writing seal format wantFormat opens root and
// recovers wantLeases identities: the question the whole repair is asked on behalf of.
func requireOpensWithFormat(t *testing.T, root string, wantFormat, wantLeases int) {
	t.Helper()

	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = wantFormat
	t.Cleanup(func() { _ = lock.Release() })

	j, err := lock.openDeliveryJournal()
	require.NoError(t, err, "seals: %s", toolTestSeals(t, root))
	j.st.Lock()
	defer j.st.Unlock()
	require.Len(t, j.leases, wantLeases)
}

// TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock is design §6.2's T31.
//
// It pins the four properties §4.5 gives the tool: it refuses while a daemon holds the lock, it
// converts both seals to v1 through the full reader and the full load, it converts only AFTER that
// load succeeds, and Rule R — the one place a position the daemon's reader refuses may be accepted
// — needs an explicit confirmation and says exactly which lines it admitted.
func TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock(t *testing.T) {
	t.Run("refuses while a daemon holds the lock", func(t *testing.T) {
		root := toolTestProject(t, 1)
		before := toolTestState(t, root)

		lock, err := acquireTestDeliveryLock(root)
		require.NoError(t, err)
		t.Cleanup(func() { _ = lock.Release() })

		for _, o := range []DeliverySealOptions{{Check: true}, {ToV1: true}} {
			_, err := toolTestRun(t, root, o)
			require.ErrorIs(t, err, ErrLockHeld,
				"a repair that raced a live daemon would be the corruption it exists to undo")
		}
		require.Equal(t, before, toolTestState(t, root))
	})

	t.Run("converts both seals to v1 after a check that writes nothing", func(t *testing.T) {
		root := toolTestProject(t, 2)
		state := paths.Of(root).State
		leaseSeal := filepath.Join(state, deliveryPositionFile)
		ackSeal := filepath.Join(state, deliveryAckPositionFile)
		for _, p := range []string{leaseSeal, ackSeal} {
			info, err := os.Lstat(p)
			require.NoError(t, err)
			require.Equal(t, int64(deliverySealFileSize), info.Size(), "%s should be a v2 image", p)
		}

		before := toolTestState(t, root)
		report, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.NoError(t, err)
		require.Contains(t, report, "v2")
		require.Contains(t, report, "nothing was written")
		require.Equal(t, before, toolTestState(t, root), "--check must write nothing")

		report, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.NoError(t, err)
		require.Contains(t, report, "wrote v1")

		leasePosition, err := loadDeliveryPosition(leaseSeal, deliveryChainSeed)
		require.NoError(t, err, "lease seal: %s", toolTestSeals(t, root))
		require.Equal(t, 2, leasePosition.Count)
		ackPosition, err := loadDeliveryPosition(ackSeal, deliveryAckChainSeed)
		require.NoError(t, err, "ack seal: %s", toolTestSeals(t, root))
		require.Equal(t, 0, ackPosition.Count)

		after := toolTestState(t, root)
		require.Equal(t, before[deliveryLeaseFile], after[deliveryLeaseFile], "the journals are evidence")
		require.Equal(t, before[deliveryAckFile], after[deliveryAckFile], "the journals are evidence")

		// The point of the conversion: a build that writes v1 — the one an operator rolled back to
		// — now opens the project and recovers every identity.
		requireOpensWithFormat(t, root, 1, 2)
	})

	t.Run("refuses to convert a journal that does not load", func(t *testing.T) {
		root := toolTestProject(t, 2)
		journal := filepath.Join(paths.Of(root).State, deliveryLeaseFile)
		info, err := os.Lstat(journal)
		require.NoError(t, err)
		require.NoError(t, os.Truncate(journal, info.Size()-1))
		before := toolTestState(t, root)

		_, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.Error(t, err)
		require.Equal(t, before, toolTestState(t, root),
			"--to v1 converts only after a successful full load")
	})

	t.Run("refuses a half-present pair", func(t *testing.T) {
		root := toolTestProject(t, 1)
		require.NoError(t, os.Remove(filepath.Join(paths.Of(root).State, deliveryAckFile)))

		_, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.Error(t, err)
	})

	t.Run("reports a project with no delivery journal", func(t *testing.T) {
		root := t.TempDir()

		report, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.NoError(t, err)
		require.Contains(t, report, "no delivery journal")

		_, err = os.Lstat(filepath.Join(paths.Of(root).State, deliveryLeaseFile))
		require.True(t, os.IsNotExist(err), "a check must not bring a journal into existence")
	})

	t.Run("a torn slot is refused without consent", func(t *testing.T) {
		root := toolTestProject(t, 2)
		tearNewestSlot(t, root)
		before := toolTestState(t, root)

		// The strict reader refuses it, exactly as the daemon's own open would.
		_, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.Error(t, err)

		// Rule R without its confirmation is refused before the project is touched at all.
		report, err := toolTestRun(t, root, DeliverySealOptions{ToV1: true, AcceptTornSlot: true})
		require.Error(t, err)
		require.Empty(t, report)

		require.Equal(t, before, toolTestState(t, root))
	})

	t.Run("a confirmed torn slot accepts the complete tail and names it", func(t *testing.T) {
		root := toolTestProject(t, 2)
		tearNewestSlot(t, root)

		report, err := toolTestRun(t, root, DeliverySealOptions{
			ToV1: true, AcceptTornSlot: true, Confirm: true,
		})
		require.NoError(t, err)

		// Exactly which lines it accepted: the second lease's, which the surviving slot does not
		// seal. The first lease's line is sealed by that slot, so it is not part of the acceptance.
		require.Contains(t, report, "admitted 1 line")
		require.Contains(t, report, testDeliveryToken('b'))
		require.NotContains(t, report, testDeliveryToken('a'))

		position, err := loadDeliveryPosition(
			filepath.Join(paths.Of(root).State, deliveryPositionFile), deliveryChainSeed)
		require.NoError(t, err)
		require.Equal(t, 2, position.Count, "the repair seals the tail it accepted")
		requireOpensWithFormat(t, root, 1, 2)
	})
}
