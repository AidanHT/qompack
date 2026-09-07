package daemon

import (
	"cmp"
	"slices"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// DropClass and its constants are ALIASES of the scheduler types, so this composition root and
// test/replay/l3policy classify identically by construction rather than by a parity test.
type DropClass = scheduler.DropClass

// The four droppable classes, re-exported so daemon code reads without the package qualifier.
const (
	DropNone       = scheduler.DropNone
	DropOrdinary   = scheduler.DropOrdinary
	DropSuperseded = scheduler.DropSuperseded
	DropEphemeral  = scheduler.DropEphemeral
)

// ClassifyDrop is the whole adapter: the classification rules live in `scheduler` so
// `test/replay/l3policy` shares them without importing this composition root.
func ClassifyDrop(rec store.ToolUseRecord) DropClass {
	return scheduler.DropClassOf(rec.Tool, rec.Ephemeral, rec.Status == store.StatusSuperseded)
}

// dropBlock is one classified block of the context prefix: where it starts, what it costs, and
// how droppable it is.
type dropBlock struct {
	Pos    int
	Tokens core.Tokens
	Class  DropClass
}

// reclaimableIndex answers "Σ tokens of droppable blocks after p" in O(log N).
//
// Computing reclaimable(p) per candidate by scanning all records would be O(N × C). A single
// ascending sweep gives O(N log N + C log N) and makes Qompack.md §5.4's monotonicity structural
// rather than hoped-for.
type reclaimableIndex struct {
	pos    []int         // ascending, deduplicated is NOT required
	suffix []core.Tokens // suffix[i] = Σ tokens of blocks[i:]
	total  core.Tokens
	counts [4]int // per-DropClass block counts, for /qompack:status
}

// newReclaimableIndex builds the index. DropNone blocks and blocks with no positive token count
// are counted but never summed; blocks is not modified.
func newReclaimableIndex(blocks []dropBlock) *reclaimableIndex {
	kept := blocks[:0:0]
	idx := &reclaimableIndex{}
	for _, b := range blocks {
		// counts is sized for the four DropClass values; a class outside them is a caller bug
		// (ClassifyDrop and DropClassOf only produce the four) and is counted nowhere rather
		// than panicking on the async path.
		if c := int(b.Class); c >= 0 && c < len(idx.counts) {
			idx.counts[c]++
		}
		if b.Class == DropNone || b.Tokens <= 0 {
			continue
		}
		kept = append(kept, b)
	}
	// slices.SortFunc rather than sort.SliceStable: generic instead of reflection-swapped, and
	// unstable, which is the difference between well under 1 ms and ~3 ms on the 5 000-block
	// build this file is benchmarked against. Stability is unobservable through After:
	// sort.SearchInts lands on the FIRST index of an equal-Pos run, and the suffix sum at that
	// index is the same under every permutation of the run.
	slices.SortFunc(kept, func(a, b dropBlock) int { return cmp.Compare(a.Pos, b.Pos) })
	idx.pos = make([]int, len(kept))
	idx.suffix = make([]core.Tokens, len(kept)+1)
	for i, b := range kept {
		idx.pos[i] = b.Pos
	}
	for i := len(kept) - 1; i >= 0; i-- {
		idx.suffix[i] = idx.suffix[i+1] + kept[i].Tokens
	}
	idx.total = idx.suffix[0]
	return idx
}

// After returns Σ tokens of droppable blocks at position >= p. Non-increasing in p by
// construction, which is exactly Qompack.md §5.4's monotonicity property.
func (r *reclaimableIndex) After(p int) core.Tokens {
	i := sort.SearchInts(r.pos, p)
	return r.suffix[i]
}
