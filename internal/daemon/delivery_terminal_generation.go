package daemon

import (
	"context"
	"errors"
	"os"
)

// Terminal evidence is durable before its generation index. An interrupted
// mirror is recoverable from the exact disposition; it never becomes capture ACK.
// Only a disposition of an ARCHIVED lease is mirrored at once: an active-window
// lease is not in the store yet, and its disposition is archived with its window
// when the segment rotates (reconcileGenerations).
func (j *deliveryJournal) commitTerminalGeneration(ctx context.Context, record deliveryTerminal) error {
	if j.gen == nil || j.leaseActive(record.Lease.Delivery) {
		return nil
	}
	return j.gen.commitTerminal(ctx, []deliveryTerminal{record})
}

// leaseActive reports whether delivery is leased in the ACTIVE window (as opposed to archived in the
// generation store, or never leased).
func (j *deliveryJournal) leaseActive(delivery string) bool {
	j.st.Lock()
	defer j.st.Unlock()
	_, ok := j.leases[delivery]
	return ok
}

// Generation-backed startup loads only the bounded active window. Archived
// dispositions are resolved by full identity when replay asks for them, rather
// than rebuilding an ever-growing terminal map from the lifetime directory.
// The active window's dispositions stay out of the store until the window is
// archived at rotation.
func (j *deliveryJournal) loadActiveTerminalDispositions() error {
	root, err := j.terminalDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return deliveryJournalError()
	}
	defer func() { _ = root.Close() }()
	loaded := make(map[string]deliveryTerminal)
	for nonce, lease := range j.leases {
		name, err := terminalFileName(lease.ObservationID)
		if err != nil {
			return err
		}
		record, err := readTerminal(root, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || record != terminalFor(lease) {
			return deliveryJournalError()
		}
		loaded[nonce] = record
	}
	j.terminal = loaded
	return nil
}

// The caller holds owner.mu and an in-flight operation, so close and rotation
// cannot invalidate the generation handle. Every membership query joins against
// the original lease before it can authorize consuming the retained input.
func (j *deliveryJournal) archivedTerminalDenied(lease deliveryLease) (bool, error) {
	ctx := context.Background()
	known, found, err := j.gen.resolveLease(ctx, lease.Delivery)
	if err != nil || !found || known != lease {
		return false, deliveryJournalError()
	}
	snapshot, err := j.gen.snapshot()
	if err != nil {
		return false, err
	}
	raw, found, err := j.gen.radix.lookup(ctx, snapshot, termGenKey(lease.Delivery))
	if err != nil {
		return false, err
	}
	if found {
		if string(raw) != string(lease.ObservationID) {
			return false, deliveryJournalError()
		}
		return true, nil
	}
	root, err := j.terminalDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, deliveryJournalError()
	}
	defer func() { _ = root.Close() }()
	name, err := terminalFileName(lease.ObservationID)
	if err != nil {
		return false, err
	}
	record, err := readTerminal(root, name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || record != terminalFor(lease) {
		return false, deliveryJournalError()
	}
	if err := j.commitTerminalGeneration(ctx, record); err != nil {
		return false, j.poison(deliveryJournalError())
	}
	return true, nil
}
