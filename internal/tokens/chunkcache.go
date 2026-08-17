package tokens

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The per-chunk token cache is what makes G10.2's accounting "exact" rather than merely "measured
// once": store.Put measures each NOVEL chunk while it still holds the plaintext, and every later
// root that references that chunk is priced from the cached measurement without re-reading a byte.
//
// The file is a flat append log with a fixed-width record, not JSON: it is written on the
// SessionEnd path and read at daemon start, so both ends have to be cheap, and a fixed stride is
// what lets a truncated tail be discarded by simple arithmetic instead of a parse.

// chunkCacheMagic opens the file so a wrong or corrupt file is rejected before its bytes are read
// as records.
const chunkCacheMagic = "QPKT"

// chunkCacheVersion is the on-disk format version.
const chunkCacheVersion = 1

// chunkCacheHeaderSize is the header's width: 4 magic bytes, a uint16 version, a uint16 reserved
// field, and a uint64 record count.
const chunkCacheHeaderSize = 16

// chunkCacheRecordSize is one record's width: a 32-byte chunk hash and a uint32 unit count.
const chunkCacheRecordSize = 36

// chunkCacheMaxEntries bounds the IN-MEMORY cache. The on-disk file may hold more: an entry
// evicted from memory is still on disk and reloads on the next construction.
//
// It is a var rather than a const solely so this package's own test can lower it and exercise
// eviction and compaction without writing a quarter of a million records. Nothing outside a test
// assigns to it.
var chunkCacheMaxEntries = 250_000

// chunkCacheEvictFraction is the share of the oldest entries dropped when the in-memory cache hits
// its cap, so eviction is amortised rather than running on every insert past the limit.
const chunkCacheEvictFraction = 4 // one quarter

// chunkCacheCompactFactor is how many times chunkCacheMaxEntries the FILE may hold before Flush
// rewrites it from the live in-memory set instead of appending to it.
const chunkCacheCompactFactor = 2

// chunkRecord is one measured chunk: its hash and its class-independent unit count.
type chunkRecord struct {
	Hash  core.Hash
	Units uint32
}

// loadChunkCache reads the cache file into memory, keeping whatever prefix is intact.
//
// A truncated or corrupt file is never an error. The cache is an optimisation — every missing
// entry simply falls back to the byte-ratio estimate and is counted as a miss — so refusing to
// start because a cache file was cut short by a crash would trade a rounding difference for an
// outage.
func (e *exact) loadChunkCache() {
	if e.cachePath == "" {
		return
	}
	b, err := os.ReadFile(paths.Long(e.cachePath))
	if err != nil {
		if !os.IsNotExist(err) {
			e.log.Loud("tokens: chunk-token cache unreadable, starting empty",
				"path", e.cachePath, "err", err)
		}
		return
	}
	if len(b) < chunkCacheHeaderSize || !bytes.HasPrefix(b, []byte(chunkCacheMagic)) {
		e.log.Loud("tokens: chunk-token cache header is not "+chunkCacheMagic+", starting empty",
			"path", e.cachePath, "bytes", len(b))
		return
	}
	if v := binary.LittleEndian.Uint16(b[4:6]); v != chunkCacheVersion {
		e.log.Loud("tokens: chunk-token cache version unsupported, starting empty",
			"path", e.cachePath, "version", int(v), "want", chunkCacheVersion)
		return
	}

	body := b[chunkCacheHeaderSize:]
	whole := len(body) / chunkCacheRecordSize
	if len(body)%chunkCacheRecordSize != 0 {
		e.log.Loud("tokens: chunk-token cache has a truncated final record, keeping the intact prefix",
			"path", e.cachePath, "records", whole, "trailing_bytes", len(body)%chunkCacheRecordSize)
	}

	e.cmu.Lock()
	defer e.cmu.Unlock()
	for i := 0; i < whole; i++ {
		rec := body[i*chunkCacheRecordSize : (i+1)*chunkCacheRecordSize]
		var h core.Hash
		copy(h[:], rec[:len(h)])
		if _, ok := e.cache[h]; ok {
			continue
		}
		e.cache[h] = binary.LittleEndian.Uint32(rec[len(h):])
		e.order = append(e.order, h)
	}
	e.evictLocked()
}

// evictLocked drops the oldest quarter of the in-memory cache once it exceeds its cap. Eviction is
// insertion-ordered rather than LRU on purpose: tracking recency would need a write on every read,
// and an evicted entry is not lost — it is still in the file and reloads next time.
//
// The caller must hold cmu.
func (e *exact) evictLocked() {
	// Looping matters on the LOAD path, not the insert path: inserts arrive one at a time and a
	// single quarter is always enough, but loadChunkCache can pull a whole file's worth of entries
	// in at once, and a single pass would leave the cache well over its cap.
	for len(e.order) > chunkCacheMaxEntries {
		drop := len(e.order) / chunkCacheEvictFraction
		if drop == 0 {
			drop = len(e.order) - chunkCacheMaxEntries
		}
		for _, h := range e.order[:drop] {
			delete(e.cache, h)
		}
		e.order = append(e.order[:0], e.order[drop:]...)
	}
}

// Flush appends every chunk measured since the last flush to the cache file, and persists the
// calibration factor. It is what store.Flush calls at SessionEnd.
func (e *exact) Flush() error { return e.flush(false) }

// Close flushes and marks the estimator closed. It satisfies io.Closer, which is how store.Close
// reaches it without tokens exporting a bespoke shutdown interface.
func (e *exact) Close() error {
	err := e.flush(true)
	e.cmu.Lock()
	e.closed = true
	e.cmu.Unlock()
	return err
}

// flush writes pending records. When the file has grown past chunkCacheCompactFactor times the
// in-memory cap it is rewritten from the live set instead, which is what keeps a long-lived
// project's cache file from growing without bound.
func (e *exact) flush(closing bool) error {
	e.cmu.Lock()
	if e.cachePath == "" || (e.closed && !closing) {
		e.cmu.Unlock()
		return nil
	}
	pending := e.pending
	e.pending = nil
	live := make([]chunkRecord, 0, len(e.cache))
	for _, h := range e.order {
		if u, ok := e.cache[h]; ok {
			live = append(live, chunkRecord{Hash: h, Units: u})
		}
	}
	path := e.cachePath
	e.cmu.Unlock()

	if len(pending) == 0 && !closing {
		return nil
	}

	if err := os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700); err != nil {
		return err
	}

	onDisk := chunkCacheFileRecords(path)
	if onDisk > chunkCacheCompactFactor*chunkCacheMaxEntries {
		return chunkCacheRewrite(path, live)
	}
	if len(pending) == 0 {
		return nil
	}
	return chunkCacheAppend(path, pending, onDisk)
}

// chunkCacheFileRecords reports how many whole records the file currently holds, or 0 when it is
// missing or unreadable.
func chunkCacheFileRecords(path string) int {
	st, err := os.Stat(paths.Long(path))
	if err != nil || st.Size() < chunkCacheHeaderSize {
		return 0
	}
	return int((st.Size() - chunkCacheHeaderSize) / chunkCacheRecordSize)
}

// chunkCacheHeader renders the 16-byte header for a file holding n records.
func chunkCacheHeader(n int) []byte {
	h := make([]byte, chunkCacheHeaderSize)
	copy(h, chunkCacheMagic)
	binary.LittleEndian.PutUint16(h[4:6], chunkCacheVersion)
	binary.LittleEndian.PutUint16(h[6:8], 0) // reserved
	binary.LittleEndian.PutUint64(h[8:16], uint64(n))
	return h
}

// encodeChunkRecords renders records into their fixed-width on-disk form.
func encodeChunkRecords(records []chunkRecord) []byte {
	out := make([]byte, 0, len(records)*chunkCacheRecordSize)
	var scratch [chunkCacheRecordSize]byte
	for _, r := range records {
		copy(scratch[:], r.Hash[:])
		binary.LittleEndian.PutUint32(scratch[len(r.Hash):], r.Units)
		out = append(out, scratch[:]...)
	}
	return out
}

// chunkCacheRewrite replaces the file with exactly the live set, through WriteAtomic.
func chunkCacheRewrite(path string, live []chunkRecord) error {
	buf := append(chunkCacheHeader(len(live)), encodeChunkRecords(live)...)
	return paths.WriteAtomic(path, buf, 0o644)
}

// chunkCacheAppend appends pending records, creating the file with a header when it does not yet
// exist, and refreshes the header's record count afterwards.
func chunkCacheAppend(path string, pending []chunkRecord, onDisk int) error {
	f, err := os.OpenFile(paths.Long(path), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return err
	}
	// A file shorter than a header — brand new, or truncated to nothing — is (re)started with one.
	if st.Size() < chunkCacheHeaderSize {
		if _, err := f.WriteAt(chunkCacheHeader(0), 0); err != nil {
			return err
		}
		onDisk = 0
	}

	at := int64(chunkCacheHeaderSize) + int64(onDisk)*chunkCacheRecordSize
	if _, err := f.WriteAt(encodeChunkRecords(pending), at); err != nil {
		return err
	}
	if _, err := f.WriteAt(chunkCacheHeader(onDisk+len(pending)), 0); err != nil {
		return err
	}
	return f.Sync()
}
