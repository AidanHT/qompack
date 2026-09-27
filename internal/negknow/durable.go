package negknow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// Two ingest paths answer someone with the record they made: IngestMCP, behind record_eliminated,
// whose answer the MCP surface documents as durable ("this tool writes a persistent record"), and
// IngestPin, the Maintainer method named for `/qompack:pin --eliminated` (the shipped slash command
// reaches IngestMCP through record_eliminated today, commands/cmd_pin.go). No daemon spool line
// stands behind either: the MCP call is forwarded to the daemon and handled there, never through the
// ingest WAL. So log.go's rule that the spool is the durability boundary does not cover them, and a
// lost tail line would be a record the agent or the user was told exists. These two therefore make
// what they acknowledge durable before they return, in the order a reader depends on:
//
//  1. syncEvidence — the evidence root they minted, through the store's publication pass, so the
//     line never names an object a power cut could take;
//  2. syncAcknowledged — the log line (its file sync) and, the first time in the ledger's lifetime,
//     the records directory, so the log's own name survives too (on POSIX a file's sync does not
//     make its directory entry durable, and openLog creates the file without syncing records/).
//
// The heuristic detector and a user statement recognized in a prompt answer no one and arrive
// through the spool; they keep the unsynced append log.go describes and pay nothing.

// syncEvidence makes a freshly minted evidence root durable before the record that names it is
// appended, when the store offers the durability half (store.PublicationSync). A zero hash (no
// evidence minted) and a store without the capability (a test double) have nothing to sync.
func (l *ledger) syncEvidence(ctx context.Context, h core.Hash) error {
	if h.IsZero() {
		return nil
	}
	ps, ok := l.deps.Store.(store.PublicationSync)
	if !ok {
		return nil
	}
	if err := ps.SyncPublication(ctx, h); err != nil {
		return fmt.Errorf("negknow: making the elimination's evidence durable: %w", err)
	}
	return nil
}

// syncAcknowledged makes the elimination log durable up to its last append — the line Record just
// wrote, or the earlier line a dedup hit returned — before the caller acknowledges it. A ledger
// whose log could not be opened for appending wrote nothing this lifetime, so every record it can
// return came from the file it read at Open and there is nothing to sync.
func (l *ledger) syncAcknowledged() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return os.ErrClosed
	}
	if l.f == nil {
		return nil
	}
	f, ok := l.f.(*os.File)
	if !ok {
		// openLog returns paths.AppendOnly's *os.File. A handle that cannot be synced cannot back an
		// acknowledgement, and saying so beats answering "recorded" for a line that is not durable.
		return fmt.Errorf("%w: negknow: the elimination log's handle cannot be synced", core.ErrDegraded)
	}
	if err := l.barriers.FileBarrier(f); err != nil {
		return fmt.Errorf("negknow: making the elimination log durable: %w", err)
	}
	if l.logNameDurable {
		return nil
	}
	if err := l.barriers.DirBarrier(filepath.Dir(logPath(l.root))); err != nil {
		return fmt.Errorf("negknow: making the elimination log's name durable: %w", err)
	}
	l.logNameDurable = true
	return nil
}
