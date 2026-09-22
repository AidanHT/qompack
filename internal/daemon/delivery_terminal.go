package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

const (
	deliveryTerminalDir   = "delivery-terminal"
	deliveryTerminalLimit = deliveryLeaseMaxLine + 1024 //nomagic:allow terminal wire metadata allowance, not a config default.
	deliveryDeniedReason  = "policy_denied"
)

var errReplayDenied = errors.New("daemon: replay retired by policy denial")

// A terminal disposition retires replay, not capture. It contains no payload,
// path or free-form error text. Its binding cannot assign a new observation.
type deliveryTerminal struct {
	Version   int           `json:"v"`
	Transform string        `json:"transform"`
	Hash      string        `json:"hash"`
	Lease     deliveryLease `json:"lease"`
	Outcome   string        `json:"outcome"`
	Reason    string        `json:"reason"`
}

func terminalFor(lease deliveryLease) deliveryTerminal {
	return deliveryTerminal{
		Version: 1, Transform: "identity/v1", Hash: "sha256/v1",
		Lease: lease, Outcome: "denied", Reason: deliveryDeniedReason,
	}
}

func terminalFileName(id core.ObservationID) (string, error) {
	h, err := core.ParseHash(string(id))
	if err != nil || h.IsZero() || h.String() != string(id) {
		return "", deliveryJournalError()
	}
	return strings.TrimPrefix(h.String(), "sha256:") + ".json", nil
}

// The singleton lock anchors the store, independently of any active journal
// generation pathname. A generation rotation cannot move terminal evidence.
func (j *deliveryJournal) terminalStateDir() string {
	return filepath.Join(filepath.Dir(filepath.Dir(j.owner.path)), "state")
}

func (j *deliveryJournal) terminalDirectory(create bool) (*os.Root, error) {
	state, err := os.OpenRoot(paths.Long(j.terminalStateDir()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = state.Close() }()
	info, err := state.Lstat(deliveryTerminalDir)
	if create && errors.Is(err, os.ErrNotExist) {
		if err = state.Mkdir(deliveryTerminalDir, 0o700); err != nil {
			return nil, err
		}
		info, err = state.Lstat(deliveryTerminalDir)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, deliveryJournalError()
	}
	root, err := state.OpenRoot(deliveryTerminalDir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, deliveryJournalError()
	}
	return root, nil
}

func readTerminal(root *os.Root, name string) (deliveryTerminal, error) {
	var result deliveryTerminal
	info, err := root.Lstat(name)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() > deliveryTerminalLimit {
		return result, deliveryJournalError()
	}
	f, err := root.Open(name)
	if err != nil {
		return result, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return result, deliveryJournalError()
	}
	data, err := io.ReadAll(io.LimitReader(f, deliveryTerminalLimit+1))
	if err != nil || len(data) > deliveryTerminalLimit || json.Unmarshal(data, &result) != nil {
		return result, deliveryJournalError()
	}
	want := terminalFor(result.Lease)
	canonical, err := json.Marshal(want)
	file, nameErr := terminalFileName(result.Lease.ObservationID)
	if err != nil || nameErr != nil || file != name || !bytes.Equal(canonical, data) {
		return result, deliveryJournalError()
	}
	return result, nil
}

// loadTerminalDispositions runs during journal open under the owner lock.
// Missing data leaves a lease pending; corrupt or unjoined data refuses open.
func (j *deliveryJournal) loadTerminalDispositions() error {
	if j.gen != nil {
		return j.loadActiveTerminalDispositions()
	}
	root, err := j.terminalDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return deliveryJournalError()
	}
	defer func() { _ = root.Close() }()
	dir, err := root.Open(".")
	if err != nil {
		return deliveryJournalError()
	}
	defer func() { _ = dir.Close() }()
	loaded := make(map[string]deliveryTerminal)
	for count := 0; ; {
		entries, readErr := dir.ReadDir(64)
		for _, entry := range entries {
			count++
			if count > deliveryLeaseMaxEntries {
				return deliveryJournalError()
			}
			// A crash may leave a WriteAtomic staging file. Only canonical
			// observation filenames can commit a disposition; other files
			// remain preserved and never count as completion.
			if len(entry.Name()) != 64+len(".json") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			record, err := readTerminal(root, entry.Name())
			if err != nil || j.leases[record.Lease.Delivery] != record.Lease {
				return deliveryJournalError()
			}
			loaded[record.Lease.Delivery] = record
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return deliveryJournalError()
		}
	}
	j.terminal = loaded
	return nil
}

// retireDenied writes durable, payload-free evidence for an existing lease.
// The caller must have established an explicit denial and exact request binding.
// The owner lock prevents release from overtaking publication of this file.
func (j *deliveryJournal) retireDenied(ctx context.Context, lease deliveryLease) error {
	if j == nil || j.owner == nil {
		return deliveryJournalError()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if !j.owner.owned() || !j.usable() {
		return deliveryJournalError()
	}
	if err := j.enterTerminalOperation(); err != nil {
		return err
	}
	defer j.leave()
	j.st.Lock()
	known, held := j.leases[lease.Delivery]
	previous, exists := j.terminal[lease.Delivery]
	j.st.Unlock()
	if !held && j.gen != nil {
		var err error
		known, held, err = j.gen.resolveLease(ctx, lease.Delivery)
		if err != nil || !held {
			return deliveryJournalError()
		}
	}
	if known != lease || lease.Delivery == "" {
		return deliveryJournalError()
	}
	record := terminalFor(lease)
	if exists {
		if previous != record {
			return deliveryJournalError()
		}
		return j.commitTerminalGeneration(ctx, record)
	}
	name, err := terminalFileName(lease.ObservationID)
	if err != nil {
		return err
	}
	root, err := j.terminalDirectory(true)
	if err != nil {
		return deliveryJournalError()
	}
	defer func() { _ = root.Close() }()
	data, err := json.Marshal(record)
	if err != nil || len(data) > deliveryTerminalLimit {
		return deliveryJournalError()
	}
	old, err := readTerminal(root, name)
	if err == nil && old != record {
		return deliveryJournalError()
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return deliveryJournalError()
	}
	// Write through the pinned directory, so replacing its pathname cannot
	// redirect a disposition write. Sync state as well as the terminal directory
	// before the caller may consume the source offset.
	if err := writeTerminalAtomic(root, name, data); err != nil {
		return j.poison(deliveryJournalError())
	}
	dir := filepath.Join(j.terminalStateDir(), deliveryTerminalDir)
	pinned, pinErr := root.Stat(".")
	current, currentErr := os.Lstat(paths.Long(dir))
	if pinErr != nil || currentErr != nil || !os.SameFile(pinned, current) {
		return j.poison(deliveryJournalError())
	}
	if err := paths.SyncDir(dir); err != nil {
		return j.poison(deliveryJournalError())
	}
	if err := paths.SyncDir(j.terminalStateDir()); err != nil {
		return j.poison(deliveryJournalError())
	}
	verified, err := readTerminal(root, name)
	if err != nil || verified != record || !j.owner.owned() {
		return j.poison(deliveryJournalError())
	}
	if err := j.commitTerminalGeneration(ctx, record); err != nil {
		return j.poison(deliveryJournalError())
	}
	j.st.Lock()
	if j.terminal == nil {
		j.terminal = make(map[string]deliveryTerminal)
	}
	if j.gen == nil || j.leases[lease.Delivery] == lease {
		j.terminal[lease.Delivery] = record
	}
	j.st.Unlock()
	return nil
}

// writeTerminalAtomic stages under the confined directory. It preserves a
// failed staging file for diagnosis; only canonical observation names count as
// dispositions on reopen. The caller syncs directories before returning success.
func writeTerminalAtomic(root *os.Root, name string, data []byte) error {
	stage := ".terminal-" + rand.Text() + ".tmp"
	f, err := root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(stage, name)
}

// terminalDenied checks a full existing lease, not just a nonce. An unavailable
// journal cannot authorize either publication or skipping the retained request.
func (j *deliveryJournal) terminalDenied(lease deliveryLease) (bool, error) {
	if j == nil || j.owner == nil {
		return false, deliveryJournalError()
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if !j.owner.owned() {
		return false, deliveryJournalError()
	}
	if err := j.enterTerminalOperation(); err != nil {
		return false, err
	}
	defer j.leave()
	if j.gen != nil {
		return j.archivedTerminalDenied(lease)
	}
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.fault != nil || j.leases[lease.Delivery] != lease {
		return false, deliveryJournalError()
	}
	disposition, exists := j.terminal[lease.Delivery]
	if exists && disposition != terminalFor(lease) {
		return false, deliveryJournalError()
	}
	return exists, nil
}

// The caller holds owner.mu. Do not wait for rotation while holding that lock:
// defer this replay instead. Counting this operation excludes rotation from its
// file publication and generation update, just as the journal pipelines do.
func (j *deliveryJournal) enterTerminalOperation() error {
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.rotating || j.fault != nil {
		return deliveryJournalError()
	}
	j.inflight++
	return nil
}

func terminalForDelivery(get func() (*deliveryJournal, error), lease deliveryLease) (bool, error) {
	if get == nil {
		return false, deliveryJournalError()
	}
	j, err := get()
	if err != nil || j == nil {
		return false, deliveryJournalError()
	}
	return j.terminalDenied(lease)
}

func retireDelivery(get func() (*deliveryJournal, error), ctx context.Context, lease deliveryLease) error {
	if get == nil {
		return deliveryJournalError()
	}
	j, err := get()
	if err != nil || j == nil {
		return deliveryJournalError()
	}
	return j.retireDenied(ctx, lease)
}

// A denial cannot retire a lease while its capture handler owns the shared seen
// key. Release temporary ownership without marking capture complete.
func (dr *drainer) retireDeniedDelivery(ctx context.Context, lease deliveryLease) error {
	key := deliveryIdentityKey(lease, true, nil)
	if dr.cfg.Seen != nil {
		completed, acquired := dr.cfg.Seen.begin(key)
		if !completed && !acquired {
			return deliveryJournalError()
		}
		if acquired {
			defer dr.cfg.Seen.finish(key, false)
		}
	}
	return retireDelivery(dr.cfg.Journal, ctx, lease)
}
