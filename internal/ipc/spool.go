package ipc

import (
	"crypto/rand"
	"encoding/hex"
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

// spoolMaxBytes bounds one writer's own spool file (D4: "the queue-and-drain degradation of §8.1
// must exist below the daemon"): a writer that keeps spooling to a daemon that never comes back
// must not grow its file without bound and fill the user's disk — that would violate §7.1 directly.
// It matches walRotateBytes so the two durability tiers share one growth ceiling.
//
// It bounds a file, not the directory. Every writer has a file of its own (newSpoolFor), so the
// directory holds one file per hook process that spooled, each with the deliveries that process
// could not hand to a daemon: the bytes a drain has to replay, and no more. (Under the pid naming
// of 0.3.0 it held one file per pid, which on Linux, where a pid is rarely reused soon, was already
// one per process.)
const spoolMaxBytes = 64 << 20 // 64 MiB per writer's file

// ErrSpoolFull is writeLocked's internal signal that this writer's own spool file has reached
// spoolMaxBytes. It is exported because it names a real, documented condition (§12.3), but no
// caller ever observes it directly: Append swallows it into the drop path exactly like any other
// write failure and always returns nil, per §12.3's "spool write fails" rule.
//
// The cap counts everything the file holds, consumed or not: a drain records its progress in
// state/drain.json, never in the file, and removes the file only once it has consumed all of it.
// A hook process spools only what its own invocation sends, each line under MaxLineBytes and far
// below the cap, so only a long-lived writer that has itself spooled 64 MiB reaches it. Until 0.3.1 the file was named by pid alone, so on Windows a
// hook that reused an earlier hook's pid appended to that hook's undrained file and shared its cap:
// a file the daemon's ordering gate held unconsumed could fill while the daemon was draining, and
// a later hook's capture was then dropped (known issue 19, D78(c)).
var ErrSpoolFull = errors.New("qompack: spool file at cap")

// spoolFilePrefix, spoolFileExt and walFilePrefix name the two families SpoolFiles distinguishes:
// the hooks' append-only client spools and the daemon's WAL segments (wal-<session>.ndjson), which
// live in the same directory and drain together. A client spool is client-<pid>-<writer id>.ndjson,
// one per writer (clientSpoolName), or client-<pid>.ndjson, the name a 0.3.0 hook gave its file,
// which an upgraded project can still hold and the daemon drains with the rest. Both are
// spoolFilePrefix ... spoolFileExt, which is all SpoolFileKindOf asks of a client spool.
const (
	spoolFilePrefix = "client-"
	spoolFileExt    = ".ndjson"
	walFilePrefix   = "wal-"
)

// spoolWriterIDBytes is the entropy of a writer id: 64 bits from crypto/rand, 16 hex characters in
// the file name. Two writers share a file only if they have the same pid and draw the same id while
// the first one's file is still undrained.
const spoolWriterIDBytes = 8

// clientSpoolName is the base name of the client spool of the writer with pid and writerID.
func clientSpoolName(pid int, writerID string) string {
	return fmt.Sprintf("%s%d-%s%s", spoolFilePrefix, pid, writerID, spoolFileExt)
}

// newSpoolWriterID draws a writer id. crypto/rand.Read never returns an error (Go 1.24 and later):
// a platform without a working source ends the process instead, so the id is always drawn.
func newSpoolWriterID() string {
	var b [spoolWriterIDBytes]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// spool is the real SpoolWriter (00-ARCHITECTURE.md §2.4, D4): the durability fallback every
// failure path in Client.Send lands on. NewSpool records dir but opens nothing — a process that
// never spools pays nothing — and Append lazily creates the directory and the file on first use,
// keeping the handle for the writer's lifetime. The file is the writer's own: its name carries the
// process's pid and an id this writer drew (newSpoolFor), so no other writer, in this process or
// in a later one that reuses the pid, appends to it.
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
//
// Its §12.3 "drop the event, Loud once" goes to a Nop, so prefer NewSpoolWithObs wherever a logger
// exists. This form stays for callers that genuinely have none.
func NewSpool(dir string) (SpoolWriter, error) {
	return newSpool(dir, logging.Nop(), nil), nil
}

// NewSpoolWithObs is NewSpool with the caller's own logger and registry, so a refused spool write
// reaches the caller's log instead of a discarded Nop.
//
// It exists because of finding F-2. internal/cli's hook path built its spool through NewSpool, and
// an ordinary write failure is the one case spool.Append handles ENTIRELY internally — it drops,
// counts and Louds, and returns nil — so the client's own drop branch never ran and the whole
// §12.3 announcement went to logging.Nop(). A project whose .qompack was made read-only underneath
// a live session therefore left no durable evidence of the degradation at all: test/platform swept
// logs/, LOUD.log, metrics/, records/, the spool and state/ and found nothing changed, with stderr
// empty.
func NewSpoolWithObs(dir string, log logging.Logger, m obs.Registry) SpoolWriter {
	return newSpool(dir, log, m)
}

// newSpool is NewSpool's body with the logger/registry a real Client already has to hand, so
// §12.3's "drop the event ... Loud once" observability reaches the caller's own log/metrics
// instead of a silently discarded Nop pair. NewClientWithOptions calls this directly; NewSpool
// (SP-01's shipped two-argument constructor, used by every caller with no Logger/Registry handy)
// wires Nop/nil.
func newSpool(dir string, log logging.Logger, m obs.Registry) *spool {
	return newSpoolFor(dir, os.Getpid(), log, m)
}

// NewSpoolForPID is NewSpoolWithObs for a writer that stands in for the process pid: its file is
// named as a writer in that process would name it, with an id of its own. It is the seam for
// harnesses and tests that play several hook processes inside one, the pid reuse of known issue 19
// among them. Every real hook's writer is NewSpoolWithObs's, under its own pid.
func NewSpoolForPID(dir string, pid int, log logging.Logger, m obs.Registry) SpoolWriter {
	return newSpoolFor(dir, pid, log, m)
}

// newSpoolFor is the one constructor: a writer for pid whose file, client-<pid>-<writer id>.ndjson,
// no other writer shares (spoolWriterIDBytes). Two writers in one process, the C1.16 rig's in-process
// hooks among them (D78(b)), and two processes with one pid, which Windows hands out again while an
// earlier hook's file waits undrained (known issue 19, D78(c)), each get a file, a record order and a
// spoolMaxBytes cap of their own.
func newSpoolFor(dir string, pid int, log logging.Logger, m obs.Registry) *spool {
	if log == nil {
		log = logging.Nop()
	}
	return &spool{
		dir:  dir,
		path: filepath.Join(dir, clientSpoolName(pid, newSpoolWriterID())),
		log:  log,
		m:    m,
	}
}

// Path returns the file Append writes to, so /qompack:status and the daemon's drain can name it.
// It is stable for the writer's lifetime, computed at construction, before any file is created, and
// it is the only way to learn it: the writer id in the name is drawn at construction.
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
			// The name is this writer's own, so the file is new; whatever it already holds still
			// counts against the cap.
			s.bytes = fi.Size()
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

// SpoolFileKind is what a file in the spool directory is, by its base name.
type SpoolFileKind int

const (
	// SpoolFileOther is anything SpoolFiles does not return: a hook's externalized tool result
	// (blob-<pid>-<n>.bin, which the daemon removes once the request naming it is published) or a
	// stray file. No drain replays it.
	SpoolFileOther SpoolFileKind = iota
	// SpoolFileWAL is one of the daemon's WAL segments (wal-<session>.ndjson).
	SpoolFileWAL
	// SpoolFileClient is a hook's client spool: client-<pid>-<writer id>.ndjson, one per writer, or
	// client-<pid>.ndjson, the name a 0.3.0 hook gave its file.
	SpoolFileClient
)

// SpoolFileKindOf classifies the spool directory entry base as SpoolFiles does.
func SpoolFileKindOf(base string) SpoolFileKind {
	switch {
	case strings.HasPrefix(base, walFilePrefix) && strings.HasSuffix(base, spoolFileExt):
		return SpoolFileWAL
	case strings.HasPrefix(base, spoolFilePrefix) && strings.HasSuffix(base, spoolFileExt):
		return SpoolFileClient
	default:
		return SpoolFileOther
	}
}

// SpoolFiles returns every spool-tier file in dir, sorted with the daemon's WAL segments
// (wal-*.ndjson) first and this package's own client spools (client-*.ndjson) after, each group by
// name. The daemon's drain reads the WAL segments in this order and puts the client spools in host
// order itself, by the timestamp of the first record each file still has to replay
// (internal/daemon, SP08-D3), because a client spool's name says nothing about when its hook ran. A
// missing directory reports an empty list rather than an error: a project that has never spooled
// anything has nothing to drain.
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
		switch SpoolFileKindOf(name) {
		case SpoolFileWAL:
			wal = append(wal, filepath.Join(dir, name))
		case SpoolFileClient:
			client = append(client, filepath.Join(dir, name))
		case SpoolFileOther:
		}
	}
	sort.Strings(wal)
	sort.Strings(client)
	return append(wal, client...), nil
}
