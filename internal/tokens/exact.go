package tokens

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// The exact estimator (00-ARCHITECTURE.md §5.20; Qompack.md G10.2) replaces the host's coarse 4/3
// padding and its flat 2 000-token images and PDFs with three measured inputs: a deterministic
// unit scanner over real bytes, real image dimensions and PDF page/text content, and a per-chunk
// cache keyed by chunk hash so a root already accounted for is never re-scanned.
//
// Everything with a runtime.tokens.* config key is read from config, never restated as a literal
// (D11, §11.6). The only two numeric constants this file introduces are unitWeight — the unit
// scanner's own per-class calibration, which has no config key because it is an implementation
// detail of the scanner rather than a tunable — and imageLongEdgeClamp in media.go.

// unitWeight converts raw scanner units into tokens, per class. The scanner approximates BPE by
// counting word-ish and punctuation-ish units; each content class then packs a slightly different
// number of real tokens into one unit. Prose is the densest (common words are single tokens);
// opaque binary is the least dense (byte soup tokenizes poorly).
var unitWeight = map[Class]float64{
	ClassProse:  0.92,
	ClassCode:   1.00,
	ClassJSON:   1.05,
	ClassDiff:   1.00,
	ClassImage:  1.00,
	ClassPDF:    1.00,
	ClassBinary: 1.35,
}

// wordUnitBytes is how many bytes of one alphanumeric run the scanner charges a single unit for.
// It is the scanner's own granularity, not a characters-per-token config value: charsPerToken
// prices a whole payload, while this splits one word into sub-word units the way BPE does.
const wordUnitBytes = 4

// isAlnum reports whether c belongs to a word run: ASCII letters, digits, underscore and the
// apostrophe (so "don't" and "snake_case" each scan as one run rather than three).
func isAlnum(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '\'':
		return true
	default:
		return false
	}
}

// isHorizontalSpace reports whether c is space, tab or carriage return — the run the scanner
// charges for only when it is long enough to be indentation rather than a word separator.
func isHorizontalSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }

// wideRuneFloor is the code point at and above which one rune costs two units instead of one.
//
// It is the UTF-8 two-byte/three-byte boundary: runes below it (Latin-1 supplement, Greek,
// Cyrillic) encode in two bytes and tokenize as roughly one token, while runes at or above it
// (CJK, most symbols and emoji) encode in three or four and tokenize as roughly two. It is a
// property of the encoding, not a tunable, so it has no config key.
const wideRuneFloor = 0x0800 //nomagic:allow the UTF-8 2-byte/3-byte boundary, not a config value

// units counts BPE-approximating units in b. It allocates nothing and is a pure function of the
// bytes, so the same content produces the same count on every platform — which is what lets the
// per-chunk cache be persisted and shared across sessions.
//
// Malformed UTF-8 is charged as a RUN, at the same ceil(n/4) rate as a word, rather than per byte.
// That is not an aesthetic choice: it is what makes units MONOTONE UNDER APPEND. Charging a lone
// lead byte as its own unit would let completing a multi-byte rune LOWER the count (a bare 0xC3
// decodes as RuneError, but 0xC3 0xA9 is U+00E9, one unit), and monotonicity in length is a
// property both this package's own property test and the tokenstest conformance suite assert.
func units(b []byte) int {
	n, i := 0, 0
	for i < len(b) {
		c := b[i]
		switch {
		case isAlnum(c):
			j := i
			for j < len(b) && isAlnum(b[j]) {
				j++
			}
			n += (j - i + wordUnitBytes - 1) / wordUnitBytes
			i = j

		case isHorizontalSpace(c):
			j := i
			for j < len(b) && isHorizontalSpace(b[j]) {
				j++
			}
			// Two spaces separate words and cost nothing; a long run is indentation, which the
			// tokenizer does charge for.
			if j-i > 2 {
				n += (j - i) / wordUnitBytes
			}
			i = j

		case c == '\n':
			n++
			i++

		case c < utf8.RuneSelf:
			n++ // each punctuation byte is its own unit
			i++

		default:
			r, sz := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && sz <= 1 {
				j := i
				for j < len(b) && b[j] >= utf8.RuneSelf {
					rr, ss := utf8.DecodeRune(b[j:])
					if rr != utf8.RuneError || ss > 1 {
						break
					}
					j++
				}
				n += (j - i + wordUnitBytes - 1) / wordUnitBytes
				i = j
				continue
			}
			if r >= wideRuneFloor {
				n += 2 // CJK and most symbols
			} else {
				n++
			}
			i += sz
		}
	}
	return n
}

// ChunkSink is the seam store.Put writes measured chunks through while it still holds the
// plaintext (00-ARCHITECTURE.md §5.8). It lives here, not in store, so that store can feed
// measurements to the estimator without tokens ever importing store (§3.2).
type ChunkSink interface {
	// NoteChunk records the class-independent unit measurement for h and returns what those units
	// cost under class c.
	NoteChunk(h core.Hash, c Class, b []byte) core.Tokens
}

// Persister is the exact estimator's durability seam beyond the frozen §5.20 Estimator interface:
// the store calls Flush at SessionEnd and Close at shutdown so the per-chunk cache and the
// calibration factor survive the process.
type Persister interface {
	Flush() error
	Close() error
}

// Compile-time proof of the four seams the store reaches this type through: the frozen §5.20
// Estimator, the ChunkSink store.Put feeds measurements to, Persister, and io.Closer, which is how
// store.Close persists the chunk cache and the calibration factor at shutdown.
//
// These are asserted here rather than left to the call site because store reaches the last three
// through RUNTIME type assertions (`if sink, ok := deps.Tokens.(tokens.ChunkSink)`). A signature
// drift would therefore not fail the build — it would silently make every chunk a cache miss and
// every Flush a no-op, which is exactly the kind of quiet degradation G10.2 exists to remove.
var (
	_ Estimator = (*exact)(nil)
	_ ChunkSink = (*exact)(nil)
	_ Persister = (*exact)(nil)
	_ io.Closer = (*exact)(nil)
)

// exact is the concrete exact Estimator.
type exact struct {
	cfg     config.RTokensCfg
	log     logging.Logger
	metrics obs.Registry

	// calib guards the calibration state; see calibrate.go.
	calib calibState

	// cmu guards every chunk-cache field below.
	cmu       sync.Mutex
	cache     map[core.Hash]uint32
	order     []core.Hash
	pending   []chunkRecord
	cachePath string
	closed    bool
}

// NewExact returns the exact Estimator (G10.2). calibPath is the calibration document to read and
// persist through (empty means in-memory only); chunkCachePath is the per-chunk token cache file
// (empty means in-memory only).
func NewExact(cfg config.Config, calibPath, chunkCachePath string) Estimator {
	return NewExactWithObs(cfg, calibPath, chunkCachePath, nil, nil)
}

// NewExactWithObs is NewExact with the observability seams wired.
//
// It is additive: §5.20 freezes New's signature, which has nowhere to pass a logger or a metrics
// registry, but the chunk-cache miss counter and the corrupt-file Loud line both need one. A nil
// log becomes logging.Nop (whose Loud calls still reach the process-wide ring and any attached
// observer), and a nil registry disables the counters rather than panicking.
func NewExactWithObs(cfg config.Config, calibPath, chunkCachePath string, log logging.Logger, metrics obs.Registry) Estimator {
	return newExact(cfg, calibPath, calibPath, chunkCachePath, log, metrics)
}

// New returns an Estimator reading its constants from cfg.Runtime.Tokens, with calibration scoped
// to calibPath itself. This is the frozen 00-ARCHITECTURE.md §5.20 signature.
//
// Prefer NewForProject. §5.20's prose requires a per-PROJECT entry keyed by
// sha256(projectRoot).Short() inside the single shared paths.Global(home)/calibration.json, and
// this signature has no project-root parameter to supply it. Passing the shared global path to New
// therefore makes every project on the machine share one calibration entry, which is precisely the
// per-project isolation §5.20 exists to provide (a Go-heavy project and a prose-heavy one have
// genuinely different characters-per-token). New is kept because §5.20 fixes it and callers that
// legitimately want file-scoped calibration — tests, and any caller with a per-project calibPath —
// are correct to use it.
func New(cfg config.Config, calibPath string) Estimator {
	return newExact(cfg, calibPath, calibPath, "", nil, nil)
}

// NewForProject returns an Estimator whose persisted calibration entry is keyed by projectRoot,
// so one shared paths.Global(home)/calibration.json holds a distinct factor per project exactly
// as §5.20 describes.
//
// This is an SP-01 addition, not a change: §5.20's New keeps its frozen signature and delegates
// here. Adding to an interface the adding subplan owns is legal under §0, and §14.0 already does
// the same thing for obs.Registry.Persist. The composition roots (cli, daemon) call this one with
// the resolved project root; an empty projectRoot falls back to keying by calibPath.
func NewForProject(cfg config.Config, calibPath, projectRoot string) Estimator {
	scope := projectRoot
	if scope == "" {
		scope = calibPath
	}
	return newExact(cfg, calibPath, scope, "", nil, nil)
}

// newExact is the single construction path every exported constructor funnels through.
func newExact(cfg config.Config, calibPath, scope, chunkCachePath string, log logging.Logger, metrics obs.Registry) Estimator {
	if log == nil {
		log = logging.Nop()
	}
	e := &exact{
		cfg:       cfg.Runtime.Tokens,
		log:       log,
		metrics:   metrics,
		cache:     make(map[core.Hash]uint32),
		cachePath: chunkCachePath,
	}
	e.calib.init(cfg.Runtime.Tokens, calibPath, scope, log)
	e.loadChunkCache()
	return e
}

// count increments a counter when a registry is wired, and does nothing when one is not.
func (e *exact) count(name string, n int64) {
	if e.metrics == nil || n == 0 {
		return
	}
	e.metrics.Counter(name).Add(n)
}

// NoteChunk records the class-independent unit measurement for h — the "exact chunk-level
// accounting" G10.2 asks for — and returns what those units cost under class c.
//
// The cached value is units, NOT tokens: the memo key stays core.Hash alone, so the same chunk
// measured once under one class reprices correctly under any other. store.Put calls this once per
// NOVEL chunk, while it still holds the plaintext, so no byte is ever scanned twice.
func (e *exact) NoteChunk(h core.Hash, c Class, b []byte) core.Tokens {
	u := units(b)

	e.cmu.Lock()
	if _, ok := e.cache[h]; !ok {
		e.cache[h] = uint32(u)
		e.order = append(e.order, h)
		e.pending = append(e.pending, chunkRecord{Hash: h, Units: uint32(u)})
		e.evictLocked()
	}
	e.cmu.Unlock()

	return core.Tokens(math.Round(float64(u) * unitWeight[c]))
}

// Estimate returns round(raw(b, c) * Factor()).
//
// Media is priced from its real dimensions or page/text content; everything else, including media
// this package could not parse, is priced by the unit scanner. Factor applies on every path,
// including media, exactly as the baseline it replaces did.
func (e *exact) Estimate(b []byte, c Class) core.Tokens {
	raw := 0.0
	switch c {
	case ClassImage:
		if t, ok := e.estimateImage(b); ok {
			raw = float64(t)
		}
	case ClassPDF:
		if t, ok := e.estimatePDF(b); ok {
			raw = float64(t)
		}
	}
	if raw == 0 {
		w := unitWeight[c]
		if c == ClassImage || c == ClassPDF {
			// Unparseable media is opaque bytes, so it is priced as opaque bytes.
			w = unitWeight[ClassBinary]
		}
		raw = float64(units(b)) * w
	}
	return core.Tokens(math.Round(raw * e.Factor()))
}

// EstimateString is Estimate([]byte(s), c); it exists so a caller holding a string never has to
// think about the []byte conversion itself.
func (e *exact) EstimateString(s string, c Class) core.Tokens {
	return e.Estimate([]byte(s), c)
}

// EstimateRoot prices a root from its per-chunk cache without re-scanning a single byte.
//
// A cache HIT reprices the stored units under c's weight and the current factor. A cache MISS
// falls back to the byte-ratio formula the SP-01 baseline used — ceil(len / charsPerToken(c)) —
// and is counted, so a persistently high tokens.chunk_miss is visible as the accounting gap it is.
// Each chunk is rounded individually, which is what makes EstimateRoot exactly additive over its
// chunks (the property the tokenstest conformance suite asserts).
func (e *exact) EstimateRoot(_ context.Context, chunks []core.ChunkRef, c Class) core.Tokens {
	if len(chunks) == 0 {
		return 0
	}
	var (
		w      = unitWeight[c]
		per    = charsPerToken(e.cfg, c)
		factor = e.Factor()
		total  core.Tokens
		misses int64
	)

	e.cmu.Lock()
	for _, ch := range chunks {
		if u, ok := e.cache[ch.Hash]; ok {
			total += core.Tokens(math.Round(float64(u) * w * factor))
			continue
		}
		misses++
		total += core.Tokens(math.Round(float64(ceilDivBytes(ch.Len, per)) * factor))
	}
	e.cmu.Unlock()

	e.count("tokens.chunk_miss", misses)
	return total
}

// DefaultCalibPath is the user-global calibration document's location: $QOMPACK_HOME when set,
// then the user's home directory, then the OS temp directory as a last resort so a machine with no
// resolvable home still gets in-process calibration rather than a hard failure.
func DefaultCalibPath() string {
	if h := os.Getenv("QOMPACK_HOME"); h != "" {
		return filepath.Join(h, calibFileName)
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, dotQompack, calibFileName)
	}
	return filepath.Join(os.TempDir(), dotQompack, calibFileName)
}

// dotQompack is the user-global runtime directory name, matching paths.Global's own convention.
const dotQompack = ".qompack"

// calibFileName is the calibration document's file name within that directory.
const calibFileName = "calibration.json"
