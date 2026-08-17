package ipc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// spoolMaxBytes bounds one client process's own spool file (D4: "the queue-and-drain degradation
// of §8.1 must exist below the daemon"). A daemon that never comes back must not let the spool
// grow without bound and fill the user's disk — that would violate §7.1 directly. It matches
// walRotateBytes so the two durability tiers share one growth ceiling.
const spoolMaxBytes = 64 << 20 // 64 MiB per client file

// ErrSpoolFull is writeLocked's internal signal that a client's own spool file has reached
// spoolMaxBytes. It is exported because it names a real, documented condition (§12.3), but no
// caller ever observes it directly: Append swallows it into the drop path exactly like any other
// write failure and always returns nil, per §12.3's "spool write fails" rule. The daemon deletes
// drained files, so the cap is only ever reached when nothing is draining — exactly the case
// where dropping is correct.
var ErrSpoolFull = errors.New("qompack: spool file at cap")

// spoolFilePrefix, spoolFileExt and walFilePrefix name the two families SpoolFiles distinguishes:
// this client's own append-only queue (client-<pid>.ndjson) and the daemon's WAL segments
// (wal-<session>.ndjson), which live in the same directory and drain together.
const (
	spoolFilePrefix = "client-"
	spoolFileExt    = ".ndjson"
	walFilePrefix   = "wal-"
)

// spool is the real SpoolWriter (00-ARCHITECTURE.md §2.4, D4): the durability fallback every
// failure path in Client.Send lands on. NewSpool records dir but opens nothing — a process that
// never spools pays nothing — and Append lazily creates the directory and the file on first use,
// keeping the handle for the process lifetime.
type spool struct {
	dir  string
	path string
	log  logging.Logger
	m    obs.Registry

	mu    sync.Mutex
	f     *os.File
	bytes int64
	// err is sticky: once Append has failed once, every subsequent Append is a cheap no-op rather
	// than repeating the failing write (§12.3 "spool write fails").
	err error

	loudOnce  sync.Once
	closeOnce sync.Once
	closeErr  error
}

// NewSpool returns a SpoolWriter rooted at dir, which is <root>/.qompack/spool in every real
// caller. Constructing always succeeds and creates nothing.
func NewSpool(dir string) (SpoolWriter, error) {
	return newSpool(dir, logging.Nop(), nil), nil
}

// newSpool is NewSpool's body with the logger/registry a real Client already has to hand, so
// §12.3's "drop the event ... Loud once" observability reaches the caller's own log/metrics
// instead of a silently discarded Nop pair. NewClientWithOptions calls this directly; NewSpool
// (SP-01's shipped two-argument constructor, used by every caller with no Logger/Registry handy)
// wires Nop/nil.
func newSpool(dir string, log logging.Logger, m obs.Registry) *spool {
	if log == nil {
		log = logging.Nop()
	}
	return &spool{
		dir:  dir,
		path: filepath.Join(dir, fmt.Sprintf("%s%d%s", spoolFilePrefix, os.Getpid(), spoolFileExt)),
		log:  log,
		m:    m,
	}
}

// Path returns the file Append writes to, so /qompack:status and the daemon's drain can name it.
// It is stable for the writer's lifetime, computed at construction, before any file is created.
func (s *spool) Path() string { return s.path }

// Append writes req as one NDJSON line (00-ARCHITECTURE.md §2.4). A line that would exceed
// MaxLineBytes is refused outright with a wrapped core.ErrBudget, leaving the spool byte-identical
// — this is the generic SpoolWriter contract ipctest's conformance suite grades. Client.Send's own
// externalize() reduces how often this triggers (it moves an oversized Event.ToolResponse to a
// side blob before Append is reached) but cannot prevent it in general — a request whose bulk
// lives in Raw rather than Event.ToolResponse still reaches Append at full size — so the caller
// must treat a non-nil return here as a real, counted drop (see client.go's appendToSpool), never
// as the size-refusal case being unreachable.
//
// Everything else — permission denied, ENOSPC, ErrSpoolFull — is §12.3's "spool write fails":
// dropped and counted every time (so l0_dropped reflects the true count of lost events even
// though the write itself is only retried once), Loud exactly once for the life of this spool,
// and reported back as nil so the hook that called Send still exits 0.
func (s *spool) Append(req Request) error {
	// EncodeRequest already returns a line terminated by exactly one '\n' (frame.go's encodeLine
	// uses json.Encoder.Encode, which appends one); line's own length already counts it, so no
	// caller here may add a second newline or double-count it against MaxLineBytes.
	line, err := EncodeRequest(req)
	if err != nil {
		return fmt.Errorf("ipc: spool: encode: %w", err)
	}
	if len(line) > MaxLineBytes {
		return fmt.Errorf("%w: spool line of %d bytes exceeds the %d byte frame limit",
			core.ErrBudget, len(line), MaxLineBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		// Sticky: the write already failed once this process, so there is no point retrying the
		// same failing I/O for every subsequent event — but each of those events is still a real
		// drop, and l0_dropped must count every one of them, not just the first.
		if s.m != nil {
			s.m.Counter(counterL0Dropped).Add(1)
		}
		return nil
	}
	if wErr := s.writeLocked(line); wErr != nil {
		s.err = wErr
		s.loudOnce.Do(func() {
			s.log.Loud("ipc: spool write failed — event dropped", "err", wErr, "path", s.path)
		})
		if s.m != nil {
			s.m.Counter(counterL0Dropped).Add(1)
		}
		return nil
	}
	return nil
}

// writeLocked appends line — already '\n'-terminated by EncodeRequest — to the spool file,
// opening (and, on first use, creating the directory for) the file if this is the first write.
// mu must be held.
func (s *spool) writeLocked(line []byte) error {
	if s.f == nil {
		if err := os.MkdirAll(paths.Long(s.dir), dirPerm); err != nil {
			return fmt.Errorf("ipc: spool: mkdir: %w", err)
		}
		w, err := paths.AppendOnly(s.path)
		if err != nil {
			return fmt.Errorf("ipc: spool: open: %w", err)
		}
		f, ok := w.(*os.File)
		if !ok {
			// paths.AppendOnly always returns *os.File today. This branch exists so a future
			// change to that contract fails loudly here rather than silently losing bytes.
			return fmt.Errorf("ipc: spool: AppendOnly returned a non-*os.File writer")
		}
		s.f = f
		if fi, statErr := f.Stat(); statErr == nil {
			s.bytes = fi.Size() // resume the running byte count across process restarts
		}
	}

	if s.bytes+int64(len(line)) > spoolMaxBytes {
		return ErrSpoolFull
	}
	n, err := s.f.Write(line)
	if err != nil {
		return err
	}
	s.bytes += int64(n)
	return nil
}

// Close closes the spool's backing file handle, if one was ever opened. It is not part of the
// SpoolWriter interface — SP-01 shipped that interface without a Close method — so a caller that
// wants to release the handle (Client.Close) reaches it through a type assertion. It tolerates
// being called more than once: the real close happens exactly once, guarded by sync.Once, and
// every later call replays the first call's result.
func (s *spool) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.f != nil {
			s.closeErr = s.f.Close()
			s.f = nil
		}
	})
	return s.closeErr
}

// ExternalizeThreshold reports the encoded-request size at or above which Client.Send moves the
// oversized member to a side blob rather than writing it inline: the smaller of the operator's
// configured accept limit and the protocol's own ceiling, so a lowered runtime.hotPath.maxPayloadBytes
// only ever tightens the threshold, never loosens it past MaxLineBytes.
func ExternalizeThreshold(cfg config.Config) int {
	return min(cfg.Runtime.HotPath.MaxPayloadBytes, MaxLineBytes)
}

// SpoolFiles returns every spool-tier file in dir, sorted with the daemon's WAL segments
// (wal-*.ndjson) first and this package's own client spools (client-*.ndjson) after — the order
// the daemon's drain reads them in, oldest durability boundary first. A missing directory reports
// an empty list rather than an error: a project that has never spooled anything has nothing to
// drain.
func SpoolFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ipc: SpoolFiles: %w", err)
	}

	var wal, client []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasPrefix(name, walFilePrefix) && strings.HasSuffix(name, spoolFileExt):
			wal = append(wal, filepath.Join(dir, name))
		case strings.HasPrefix(name, spoolFilePrefix) && strings.HasSuffix(name, spoolFileExt):
			client = append(client, filepath.Join(dir, name))
		}
	}
	sort.Strings(wal)
	sort.Strings(client)
	return append(wal, client...), nil
}
