package contract

import (
	"bytes"
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
