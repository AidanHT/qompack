package contract

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// sentinelDomain domain-separates the sentinel token digest from every other hash the process
// produces (00-ARCHITECTURE.md §5.19's hook.additional_context_delivered mechanism).
const sentinelDomain = "qompack.sentinel.v1"

// sentinelPrefix is the fixed prefix of every minted sentinel token.
const sentinelPrefix = "qompack-contract-"

// Sentinel is a one-shot probe token SessionStart mints and emits inside additionalContext; the
// following UserPromptSubmit scans the transcript tail for it to prove additionalContext actually
// reached the transcript rather than being silently dropped by the host (§12.1's
// hook.additional_context_delivered).
type Sentinel struct {
	Token string
}

// MintSentinel derives a 12-hex-char token from sess and now, domain-separated so it can never
// collide with any other digest the process produces:
// "qompack-contract-" + core.HashBytes(sentinelDomain, sess+ts).Short().
func MintSentinel(sess core.SessionID, now core.UnixMilli) Sentinel {
	seed := string(sess) + strconv.FormatInt(int64(now), 10)
	h := core.HashBytes(sentinelDomain, []byte(seed))
	return Sentinel{Token: sentinelPrefix + h.Short()}
}

// RenderSentinel renders s as an inert HTML comment, on its own line, so it is invisible in any
// rendering path and unmistakable when grepped out of a transcript.
func RenderSentinel(s Sentinel) string {
	return "<!-- qompack-contract-probe " + s.Token + " -->"
}

// ScanTranscriptTail opens path, seeks to max(0, size-tailBytes), reads to EOF, and reports whether
// token appears in that tail. A missing or unreadable file returns (false, err); every caller in
// this package treats that as "no observation yet", never as a failure — a transcript that cannot
// be read proves nothing about whether the sentinel was ever emitted.
func ScanTranscriptTail(path, token string, tailBytes int64) (bool, error) {
	tail, err := readTail(path, tailBytes)
	if err != nil {
		return false, err
	}
	return bytes.Contains(tail, []byte(token)), nil
}

// TranscriptSize is how many bytes the transcript at path holds now, or 0 when it cannot be read —
// a transcript the host has not created yet (it writes the first line after SessionStart:startup
// in -p mode), or any other failure. The daemon records it when it mints a sentinel
// (SentinelState.ScanFrom): the host appends the SessionStart answer that carries the probe after
// that point, so ScanTranscriptForProbe can look there however much the transcript grows later.
func TranscriptSize(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(paths.Long(path))
	if err != nil || !fi.Mode().IsRegular() {
		return 0
	}
	return fi.Size()
}

// ScanTranscriptForProbe reports whether token appears in one of two bounded windows of the
// transcript at path: the window bytes from offset from — where the transcript ended when the probe
// was minted, so the SessionStart answer carrying it lands just after — and the transcript's last
// window bytes, the only place ScanTranscriptTail looks.
//
// The first window is what keeps an ordinary large tool result from hiding the probe (V6 close-out,
// Phase 4 retrieval D4): the probe sits near the start of what the host appended after the mint,
// and a single 363 KB Read in the first turn pushed it more than a tail window away from the end
// before the next prompt scanned for it, so both chances missed and the session went passive. The tail window keeps finding a probe whose mint offset is unknown
// (a history an older daemon wrote, where from is 0 but the mint was mid-transcript) wherever the
// tail alone found it before. A from past the end of the file — a transcript that was replaced or
// truncated since the mint — scans from 0.
//
// The read is bounded at 2×window bytes whatever the transcript's size. Errors are
// ScanTranscriptTail's: a transcript that cannot be read returns (false, err), which proves nothing.
func ScanTranscriptForProbe(path, token string, from, window int64) (bool, error) {
	if window <= 0 {
		return false, nil
	}
	f, err := os.Open(paths.Long(path))
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	size := info.Size()
	if from < 0 || from > size {
		from = 0
	}
	needle := []byte(token)
	head := make([]byte, min(window, size-from))
	if _, err := f.ReadAt(head, from); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if bytes.Contains(head, needle) {
		return true, nil
	}
	tailStart := max(size-window, 0)
	if tailStart >= from && from+int64(len(head)) >= size {
		return false, nil // the head window already read every byte of the tail window
	}
	tail := make([]byte, size-tailStart)
	if _, err := f.ReadAt(tail, tailStart); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.Contains(tail, needle), nil
}

// readTail reads the last n bytes of the file at path (the whole file if it is smaller than n).
// Shared by ScanTranscriptTail and the transcript.readable assertion's last-line probe, so both
// read transcripts the same bounded way rather than loading an arbitrarily large file into memory
// on every SessionStart.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(paths.Long(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := info.Size() - n
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
