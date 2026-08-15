package sketch

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/qompack/qompack/internal/core"
)

// counterBytes is the encoded width of one Count-Min counter. Appendix A sizes the table "@ 4-byte
// counters", and the number is named rather than spelled inline because the encoder, the decoder
// and the body-length check must provably agree on it: a body sized at four bytes per cell and read
// back at eight would decode into a table half the width the header declares, which every length
// check would still accept.
const counterBytes = 4

// The clamping bounds NewCMS applies. They exist because a constructor in this package never panics
// and never returns an error (util.go): every out-of-range, NaN or infinite argument becomes the
// nearest legal value instead, and these name what "legal" is.
const (
	// minCMSEpsilon is the floor on ε. width is ⌈e/ε⌉, so 1e-5 already asks for a 271 829-column
	// table — an order of magnitude finer than any touch-frequency workload justifies.
	minCMSEpsilon = 1e-5
	// maxCMSEpsilon is the ceiling on ε. Above 0.5 the additive error exceeds half the stream's
	// total mass and the estimate stops carrying information, so the nearest USEFUL legal value is
	// the right correction. Dims reports what the caller actually got.
	maxCMSEpsilon = 0.5
	// minCMSDelta is the floor on δ. depth is ⌈ln(1/δ)⌉, so 1e-9 asks for 21 rows; MaxCMSDepth
	// bounds it independently in case that formula is ever changed.
	minCMSDelta = 1e-9
	// maxCMSDelta is the ceiling on δ. At 0.5 the depth is 1 — a single row, no min over rows,
	// which is the degenerate Count-Min and still a valid one.
	maxCMSDelta = 0.5
)

// maxTotalFloat bounds Scale's float→uint64 conversion. A direct comparison against math.MaxUint64
// is unsafe: 2^64−1 is not representable in float64, so the comparison would be made against
// 2^64 after rounding and uint64(2^64) is undefined in Go. 2^62 round-trips exactly and is far
// above any total a session can accumulate.
const maxTotalFloat = float64(1 << 62)

// The on-wire param names. They are lower case because the QPKS frame restricts param names to
// [a-z0-9.] (header.go), and they are constants rather than literals at four call sites because the
// encoder and the decoder have to agree: a typo in one of them would produce a table that saves
// cleanly and then fails to load, which is the one failure §6.2 exists to prevent.
const (
	// paramDelta is the configured failure probability δ.
	paramDelta = "delta"
	// paramDepth is the number of rows, ⌈ln(1/δ)⌉.
	paramDepth = "depth"
	// paramEpsilon is the configured error factor ε.
	paramEpsilon = "epsilon"
	// paramWidth is the number of columns per row, ⌈e/ε⌉.
	paramWidth = "width"
)

// CMS is the Count-Min sketch behind sketches/touch.cms (00-ARCHITECTURE.md §5.7, Appendix A):
// approximate per-key touch-frequency counting in a fixed 53 KiB, whatever the key count.
//
// Its guarantee is one-sided. Estimate never under-counts, and over-counts by at most ε·N for at
// least 1 − δ of the keys, where N is Total. That direction is what makes the sketch safe to act
// on: the scheduler treats a high estimate as evidence to check, not as a fact, and a sketch that
// could under-count would let a hot file read as cold with nothing downstream able to tell.
//
// MergeFrom and Scale exist for Phase 7's cross-session warm start (O4): the historical table is
// merged in and then decayed, so a project's past hot files inform the session without outvoting
// what the agent is touching now.
//
// CMS is NOT safe for concurrent use. Add mutates the counter table in place and Total is not
// atomic; the daemon (SP-05) owns every live sketch behind its session-registry mutex.
type CMS struct {
	width, depth   int
	cells          []uint32 // row-major, len == width*depth, row 0 first
	total          uint64
	epsilon, delta float64
	created        core.UnixMilli
}

// NewCMS returns a CMS sized by Appendix A's formulas for error factor epsilon and failure
// probability delta:
//
//	width = ⌈e/ε⌉      depth = ⌈ln(1/δ)⌉
//
// For the Appendix C defaults (ε = 0.001, δ = 0.01) that is width = ⌈2718.281828…⌉ = 2719,
// depth = ⌈4.60517…⌉ = 5, and a body of 2719 × 5 × 4 = 54 380 bytes ≈ 53.1 KiB. Appendix A writes
// "2718 × 5 ≈ 54 KB": 2718 is the same quantity shown to display precision, and the FORMULA is
// normative while the illustration is not, so this implements ⌈e/ε⌉.
//
// It never panics and never returns an error. Arguments are clamped, not rejected: both go through
// clamp rather than a pair of comparisons precisely because a NaN must become the floor rather than
// flowing into math.Ceil and then into a make() length, which is undefined in Go and would crash
// the observer hot path (§12.3). Dims reports the dimensions the clamped pair produced.
//
// The halving loop and the depth cap together enforce the ceiling rule (errors.go): the largest
// table a constructor will build must still marshal to a frame under MaxFrameBytes.
func NewCMS(epsilon, delta float64) *CMS {
	eps := clamp(epsilon, minCMSEpsilon, maxCMSEpsilon)
	dlt := clamp(delta, minCMSDelta, maxCMSDelta)

	width := int(math.Ceil(math.E / eps))
	depth := clampInt(int(math.Ceil(math.Log(1/dlt))), 1, MaxCMSDepth)
	for width*depth > MaxCMSCells {
		width /= 2
	}
	if width < 1 {
		width = 1
	}

	return &CMS{
		width:   width,
		depth:   depth,
		cells:   make([]uint32, width*depth),
		epsilon: eps,
		delta:   dlt,
	}
}

// Add records n more occurrences of key. A weight of 0 is a no-op: it carries no information, and
// letting it move Total would loosen the ε·N bound every accuracy claim above is stated against.
//
// The depth probe positions come from Kirsch-Mitzenmacher double hashing, g_j(x) = h1 + j·h2, over
// the single domain-separated SHA-256 hash128 returns. That is what makes a depth-5 table cost one
// hash rather than five, which is what keeps SP-08's observer hook inside its §8.1 budget.
//
// Every counter update SATURATES rather than wraps. Wrapping would break the sketch's only exact
// guarantee — Estimate(k) ≥ true(k) — by turning a maximal counter into a near-zero one, and no
// consumer could detect it from the outside.
func (c *CMS) Add(key []byte, n uint32) {
	if n == 0 || c.width < 1 || c.depth < 1 {
		// The zero CMS has no counter table, and width 0 would make the modulus below divide by
		// zero. Silently doing nothing is the only non-panicking answer, and it agrees with
		// Estimate, which reports 0 for everything.
		return
	}
	h1, h2 := hash128(domainCMS, key)
	for j := 0; j < c.depth; j++ {
		idx := c.cellIndex(j, h1, h2)
		if c.cells[idx] > math.MaxUint32-n {
			c.cells[idx] = math.MaxUint32
		} else {
			c.cells[idx] += n
		}
	}
	c.total = satAdd64(c.total, uint64(n))
}

// Estimate returns the minimum of key's depth counters, which is an upper bound on its true count:
// every row counts key's own mass plus whatever collided with it, so the smallest row is the one
// least polluted. Per §13 invariant 3 the answer is a cache, never the source of truth.
func (c *CMS) Estimate(key []byte) uint32 {
	if c.width < 1 || c.depth < 1 {
		return 0
	}
	h1, h2 := hash128(domainCMS, key)
	// Named best, not min: min is a predeclared identifier in Go 1.21+, and shadowing it inside a
	// hot loop is exactly the kind of thing revive and gocritic flag.
	best := uint32(math.MaxUint32)
	for j := 0; j < c.depth; j++ {
		if v := c.cells[c.cellIndex(j, h1, h2)]; v < best {
			best = v
		}
	}
	return best
}

// cellIndex maps row j and a key's two hash words to a cell of the row-major table. It is one
// function rather than the same expression written twice because Add and Estimate MUST agree on it
// exactly: a difference between them would not fail any test of the framing or the arithmetic, it
// would simply make every estimate 0.
func (c *CMS) cellIndex(j int, h1, h2 uint64) int {
	return j*c.width + int((h1+uint64(j)*h2)%uint64(c.width))
}

// Dims returns the table's width and depth AFTER clamping. Reporting the realised dimensions rather
// than the requested (ε, δ) is what makes a silently corrected configuration visible.
func (c *CMS) Dims() (width, depth int) { return c.width, c.depth }

// Total returns N, the sum of every weight added. It is the quantity the ε·N error bound is stated
// against, so a consumer reasoning about accuracy needs it alongside any estimate.
func (c *CMS) Total() uint64 { return c.total }

// SetCreated stamps the construction time carried in the on-disk header. It is a setter rather than
// a NewCMS argument because this package takes no core.Clock: the composition root that owns the
// clock stamps the sketch, and every test that needs a byte-stable frame can pin the value without
// a fake clock.
func (c *CMS) SetCreated(t core.UnixMilli) { c.created = t }

// MergeFrom adds o's counters into c cell by cell. It is Phase 7's O4 warm-start primitive —
// "Warm-start Count-Min with the project's historical hot-file distribution" — and it is exact:
// merging two tables produces precisely the table a single pass over both streams would have
// produced, so a warm-started session answers identically to a cold one over the same data.
//
// The shapes must match exactly. Two Count-Min tables of different widths index the same key to
// different cells, so adding them elementwise would produce counters describing no stream at all —
// and every answer out of the result would still look like a plausible frequency, which is why this
// is a refusal rather than a best effort. A nil source is the same refusal, not a panic.
func (c *CMS) MergeFrom(o *CMS) error {
	if o == nil {
		return fmt.Errorf("%w: MergeFrom(nil)", ErrShapeMismatch)
	}
	if c.width != o.width || c.depth != o.depth {
		return fmt.Errorf("%w: cannot merge %dx%d into %dx%d",
			ErrShapeMismatch, o.width, o.depth, c.width, c.depth)
	}
	for i, v := range o.cells {
		if c.cells[i] > math.MaxUint32-v {
			c.cells[i] = math.MaxUint32
		} else {
			c.cells[i] += v
		}
	}
	c.total = satAdd64(c.total, o.total)
	return nil
}

// Scale multiplies every counter by factor, which is the exponential decay half of O4: a
// warm-started table must weight history BELOW the current session, or a project's historical hot
// files would outvote what the agent is touching right now for the whole of the session.
//
// The factor is clamped to [0, maxTotalFloat]. A negative or NaN factor behaves as 0 — discard
// history — because those are the only answers that keep the counters a count; the upper clamp
// exists solely so the float→uint64 conversion is defined, and no realistic decay factor comes near
// it. A factor of exactly 1 returns immediately: the identity is the common case on a session that
// declined to decay, and a no-op must not cost a pass over 54 KiB of counters.
func (c *CMS) Scale(factor float64) {
	f := clamp(factor, 0, maxTotalFloat)
	if f == 1 {
		return
	}
	for i, v := range c.cells {
		s := math.Round(float64(v) * f)
		if s > math.MaxUint32 {
			s = math.MaxUint32
		}
		c.cells[i] = uint32(s)
	}
	t := math.Round(float64(c.total) * f)
	if t > maxTotalFloat {
		t = maxTotalFloat
	}
	c.total = uint64(t)
}

// HeavyHitters pairs the keyless Count-Min table with a Misra-Gries candidate set: MG supplies the
// keys — it stores them verbatim and reports no false positives — and CMS supplies the sharper
// frequency estimate for each. The result is ordered by count descending, then key ascending, so
// "the five hottest files" is a stable answer rather than a map's iteration order.
//
// It reports nil when mg is nil or n <= 0, since neither leaves a key to report. It reports nil for
// every other input too, for exactly as long as MisraGries.Top does — the candidate set is the only
// source of keys here, because a Count-Min sketch never stores one. The Misra-Gries summary lands
// one commit after this table, and this function gains its body with it.
func (c *CMS) HeavyHitters(mg *MisraGries, n int) []Counted { return nil }

// Header returns the on-disk metadata for this table: Kind KindCMS, the current Total as Count, the
// Created stamp, and the four params that make the frame self-describing without consulting config.
// CRC32C is zero, as Sketch documents — the checksum covers the encoded body and is known only to
// DecodeHeader.
func (c *CMS) Header() Header {
	return Header{
		Magic: HeaderMagic,
		Ver:   FormatVersion,
		Kind:  KindCMS,
		Params: map[string]float64{
			paramDelta:   c.delta,
			paramDepth:   float64(c.depth),
			paramEpsilon: c.epsilon,
			paramWidth:   float64(c.width),
		},
		Count:   c.total,
		Created: c.created,
	}
}

// MarshalBinary encodes the table as a QPKS frame: the header above, then width×depth×4 bytes of
// counters, row-major with row 0 first and each counter little-endian. EncodeHeader sorts the
// params, so a given logical state has exactly one encoding — which is what makes the frozen golden
// fixtures and the byte-identical re-marshal assertion mean anything.
//
// A nil receiver reports ErrMalformed rather than dereferencing. §12.3: a hook that dies takes
// observability down with it, so every entry point in this package fails by reporting.
func (c *CMS) MarshalBinary() ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: MarshalBinary on a nil *CMS", ErrMalformed)
	}
	body := make([]byte, len(c.cells)*counterBytes)
	for i, v := range c.cells {
		binary.LittleEndian.PutUint32(body[i*counterBytes:], v)
	}
	return EncodeHeader(c.Header(), body)
}

// UnmarshalBinary replaces c's entire state with the table encoded in data, or leaves c untouched
// and reports why it could not.
//
// The version switch is the upgrade path header.go documents: case 1 decodes the layout this build
// writes, and when FormatVersion ever becomes 2 a case 2 arm is added while case 1 stays forever,
// because sketches are permanent memory (§6.2) and the oldest file on disk must still decode.
// DecodeHeader has already refused anything above FormatVersion, so the default arm is reachable
// only if that guarantee is ever broken — which is exactly when a bare sentinel beats a panic.
//
// A nil receiver reports ErrMalformed rather than panicking, for the same reason MarshalBinary does.
func (c *CMS) UnmarshalBinary(data []byte) error {
	if c == nil {
		return fmt.Errorf("%w: UnmarshalBinary on a nil *CMS", ErrMalformed)
	}
	h, body, err := DecodeHeader(data)
	if err != nil {
		return err
	}
	if h.Kind != KindCMS {
		return fmt.Errorf("%w: frame declares %s, want %s", ErrKindMismatch, h.Kind, KindCMS)
	}
	switch h.Ver {
	case 1:
		return c.decodeV1(h, body)
	default:
		return fmt.Errorf("%w: version %d", ErrUnsupportedVersion, h.Ver)
	}
}

// decodeV1 decodes the FormatVersion 1 layout.
//
// Every param is validated against the same ceilings the constructor clamps to BEFORE a single cell
// is allocated, because a forged header is the whole of this function's threat model: width×depth
// is the length of the make() below, so a frame claiming 8 388 608 × 64 has to be refused here
// rather than turned into a two-gigabyte allocation and an out-of-memory kill of the daemon. The
// body-length check comes last, so a frame that is wrong in two ways reports the structural problem
// (ErrMalformed) rather than the downstream length disagreement it causes (ErrTruncated).
func (c *CMS) decodeV1(h Header, body []byte) error {
	depth, err := h.MustParamInt(paramDepth, 1, MaxCMSDepth)
	if err != nil {
		return err
	}
	width, err := h.MustParamInt(paramWidth, 1, MaxCMSCells)
	if err != nil {
		return err
	}
	if width*depth > MaxCMSCells {
		return fmt.Errorf("%w: %d x %d cells exceeds MaxCMSCells (%d)",
			ErrMalformed, width, depth, MaxCMSCells)
	}
	// ε and δ are not integers, so MustParamInt cannot carry them. The band is (0, 1) rather than
	// the constructor's clamps: a file written by a build with different clamps must still load,
	// and neither value is used to size anything — they are reported, and they are what makes the
	// frame self-describing.
	epsilon, err := cmsRateParam(h, paramEpsilon)
	if err != nil {
		return err
	}
	delta, err := cmsRateParam(h, paramDelta)
	if err != nil {
		return err
	}
	if len(body) != width*depth*counterBytes {
		return fmt.Errorf("%w: body of %d bytes cannot hold %d x %d counters",
			ErrTruncated, len(body), width, depth)
	}

	// The body is a subslice of the caller's buffer (DecodeHeader), so this copies rather than
	// retaining it.
	cells := make([]uint32, width*depth)
	for i := range cells {
		cells[i] = binary.LittleEndian.Uint32(body[i*counterBytes:])
	}

	c.width = width
	c.depth = depth
	c.cells = cells
	c.total = h.Count
	c.epsilon = epsilon
	c.delta = delta
	c.created = h.Created
	return nil
}

// cmsRateParam reads a rate param that must lie strictly inside (0, 1). It is shared by ε and δ so
// that the two cannot drift apart: both are probabilities, both are reported rather than acted on,
// and a NaN in either would propagate into every accuracy statement made about the decoded table.
func cmsRateParam(h Header, name string) (float64, error) {
	v, ok := h.Param(name)
	if !ok {
		return 0, fmt.Errorf("%w: header has no param %q", ErrMalformed, name)
	}
	if math.IsNaN(v) || v <= 0 || v >= 1 {
		return 0, fmt.Errorf("%w: param %q = %v is not in (0, 1)", ErrMalformed, name, v)
	}
	return v, nil
}
