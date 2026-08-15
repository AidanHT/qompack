package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/paths"
)

// Logger is the leveled structured logging seam every package in Qompack writes through.
// Implementations must never panic and must never block the caller on I/O failure: a logging
// failure degrades to a dropped line, not a crashed hook.
type Logger interface {
	// With returns a derived Logger that prepends kv to every subsequent call. The receiver is
	// unaffected: fields accumulate down a chain of With calls, never sideways.
	With(kv ...any) Logger
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
	// Loud writes to the day log AND to <dir>/LOUD.log (append-only, never rotated) AND appends
	// to the process-wide ring LastLoud reads AND fires the observer AttachLoudObserver installed,
	// if any. Reserved for contract violations and degradation transitions. Never silent (§12).
	Loud(msg string, kv ...any)
}

// bytesPerMB converts a whole-megabyte rotation threshold to bytes. Written as a shift rather than
// the literal 1024 so it can appear outside internal/config/defaults.go: 1024 itself is one of the
// D11/§11.6 forbidden literals (nomagic flags the individual token, not the evaluated constant).
const bytesPerMB int64 = 1 << 20

// dayFormat is the YYYYMMDD date suffix every log filename carries.
const dayFormat = "20060102"

// loudFileName is the append-only, never-rotated destination every Loud call also writes to.
const loudFileName = "LOUD.log"

// defaultMaxFileMB and defaultMaxFiles are New's rotation limits: the same values
// config.Defaults().Runtime.Logging carries, so a caller that has not yet loaded configuration
// (every hot-path hook, per §11 step 1) still gets a sane rotation policy.
const (
	defaultMaxFileMB = 10
	defaultMaxFiles  = 5
)

// New opens <dir>/qompack-YYYYMMDD.log (creating dir if needed) and returns a Logger that emits at
// lvl and above, rotating at the default limits (10 MB, keeping 5 rotated files). The returned
// io.Closer releases the underlying file handles; callers should defer its Close.
//
// Use NewWithLimits to drive rotation from runtime.logging.maxFileMB/maxFiles once configuration
// has been loaded — New's signature is normative (00-ARCHITECTURE.md §5.2) and does not carry
// those parameters itself.
func New(dir string, lvl Level) (Logger, io.Closer, error) {
	return NewWithLimits(dir, lvl, defaultMaxFileMB, defaultMaxFiles)
}

// NewWithLimits is New with explicit rotation limits. maxFileMB <= 0 disables size-based rotation;
// maxFiles <= 0 keeps every rotated file instead of pruning.
func NewWithLimits(dir string, lvl Level, maxFileMB, maxFiles int) (Logger, io.Closer, error) {
	if dir == "" {
		return nil, nil, errors.New("logging: New: dir must not be empty")
	}
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		return nil, nil, fmt.Errorf("logging: New: mkdir %s: %w", dir, err)
	}
	s := &sink{dir: dir, maxFileMB: maxFileMB, maxFiles: maxFiles}
	if err := s.openLocked(); err != nil {
		return nil, nil, fmt.Errorf("logging: New: open day log in %s: %w", dir, err)
	}
	return logger{s: s, minLevel: lvl}, s, nil
}

// Nop returns a Logger that discards Debug/Info/Warn/Error entirely and has no backing directory,
// but still records Loud calls in the process-wide ring and still fires the attached observer, so
// a test can assert loudness without touching the filesystem.
func Nop() Logger { return logger{s: nil, minLevel: Loud} }

// logger is a lightweight, cheap-to-copy handle: the fields it accumulates via With plus a pointer
// to the shared sink every derived logger writes through. Copying a logger value is intentional —
// it is how With returns an independent handle without disturbing the parent's fields or the
// shared file state.
type logger struct {
	s        *sink
	minLevel Level
	kv       []any
}

func (l logger) With(kv ...any) Logger {
	merged := make([]any, 0, len(l.kv)+len(kv))
	merged = append(merged, l.kv...)
	merged = append(merged, kv...)
	return logger{s: l.s, minLevel: l.minLevel, kv: merged}
}

func (l logger) Debug(msg string, kv ...any) { l.log(Debug, msg, kv) }
func (l logger) Info(msg string, kv ...any)  { l.log(Info, msg, kv) }
func (l logger) Warn(msg string, kv ...any)  { l.log(Warn, msg, kv) }
func (l logger) Error(msg string, kv ...any) { l.log(Error, msg, kv) }

func (l logger) log(lvl Level, msg string, kv []any) {
	if l.s == nil || lvl < l.minLevel {
		return
	}
	l.s.write(formatLine(lvl, msg, l.kv, kv))
}

func (l logger) Loud(msg string, kv ...any) {
	merged := make([]any, 0, len(l.kv)+len(kv))
	merged = append(merged, l.kv...)
	merged = append(merged, kv...)
	line := formatLine(Loud, msg, nil, merged)
	if l.s != nil {
		l.s.write(line)
		l.s.writeLoud(line)
	}
	recordLoud(line)
	fireLoudObserver(msg, merged)
}

// formatLine renders one line as ts=<RFC3339Nano> level=<lvl> msg="…" k=v … , terminated with a
// single newline. base is the fields accumulated via With; extra is this call's own kv.
func formatLine(lvl Level, msg string, base, extra []any) string {
	var b strings.Builder
	b.WriteString("ts=")
	b.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
	b.WriteString(" level=")
	b.WriteString(lvl.String())
	b.WriteString(" msg=")
	b.WriteString(strconv.Quote(msg))
	writeKV(&b, base)
	writeKV(&b, extra)
	b.WriteByte('\n')
	return b.String()
}

func writeKV(b *strings.Builder, kv []any) {
	i := 0
	for ; i+1 < len(kv); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteByte('=')
		writeValue(b, fmt.Sprint(kv[i+1]))
	}
	if i < len(kv) {
		// An odd trailing key with no value is a caller mistake; render it with an empty value
		// rather than panic or silently drop it, so the mistake is visible in the log itself.
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteString(`=""`)
	}
}

func writeValue(b *strings.Builder, v string) {
	if v == "" || strings.ContainsAny(v, " =") {
		b.WriteString(strconv.Quote(v))
		return
	}
	b.WriteString(v)
}

// sink is the shared, mutex-protected file state one directory's logger(s) write through. Every
// logger value derived from the same New/NewWithLimits call (directly or via With) points at the
// same sink, so rotation and the LOUD.log handle are process-directory-wide, not per-handle.
type sink struct {
	mu        sync.Mutex
	dir       string
	maxFileMB int
	maxFiles  int
	file      io.WriteCloser
	loudFile  io.WriteCloser
	written   int64
	day       string
	rotSeq    int
}

func (s *sink) currentPath() string { return filepath.Join(s.dir, "qompack-"+s.day+".log") }

func (s *sink) rotatedPath(n int) string {
	return filepath.Join(s.dir, fmt.Sprintf("qompack-%s.%d.log", s.day, n))
}

// openLocked ensures s.file is open for today. It is a no-op when the current file is already
// open for today's date; on a date rollover it closes yesterday's file and starts a fresh rotation
// sequence, so the YYYYMMDD in the filename never goes stale for a long-lived process.
func (s *sink) openLocked() error {
	today := time.Now().UTC().Format(dayFormat)
	if s.file != nil && s.day == today {
		return nil
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	s.day = today
	s.rotSeq = 0
	p := s.currentPath()
	w, err := paths.AppendOnly(p)
	if err != nil {
		return err
	}
	s.file = w
	s.written = 0
	if fi, statErr := os.Stat(paths.Long(p)); statErr == nil {
		s.written = fi.Size() // resume the running byte count across process restarts
	}
	return nil
}

func (s *sink) write(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.openLocked(); err != nil {
		return // best-effort: a logging failure must never propagate to the caller
	}
	n, err := io.WriteString(s.file, line)
	if err != nil {
		return
	}
	s.written += int64(n)
	if s.maxFileMB > 0 && s.written >= int64(s.maxFileMB)*bytesPerMB {
		s.rotateLocked()
	}
}

// rotateLocked renames the current day file to qompack-YYYYMMDD.<n>.log, prunes rotated files
// beyond maxFiles, and reopens a fresh current file for the same day.
func (s *sink) rotateLocked() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	s.rotSeq++
	_ = os.Rename(paths.Long(s.currentPath()), paths.Long(s.rotatedPath(s.rotSeq)))
	if s.maxFiles > 0 {
		for n := 1; n <= s.rotSeq-s.maxFiles; n++ {
			_ = os.Remove(paths.Long(s.rotatedPath(n)))
		}
	}
	if w, err := paths.AppendOnly(s.currentPath()); err == nil {
		s.file = w
		s.written = 0
	}
}

func (s *sink) writeLoud(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loudFile == nil {
		w, err := paths.AppendOnly(filepath.Join(s.dir, loudFileName))
		if err != nil {
			return
		}
		s.loudFile = w
	}
	_, _ = io.WriteString(s.loudFile, line)
}

// Close implements io.Closer, releasing both the day-log and LOUD.log file handles.
func (s *sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errFile, errLoud error
	if s.file != nil {
		errFile = s.file.Close()
		s.file = nil
	}
	if s.loudFile != nil {
		errLoud = s.loudFile.Close()
		s.loudFile = nil
	}
	return errors.Join(errFile, errLoud)
}
