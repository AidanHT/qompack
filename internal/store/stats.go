package store

import "context"

// sketchSignatureKey is the name Stats.Sketches files the signature count under.
//
// Stats.Sketches is documented as "each sketch name → its current size in bytes", but the bloom,
// count-min and HLL sketches are the DAEMON's files, not L1's: nothing in internal/store writes or
// owns them. What the store does hold is the per-root MinHash signature that near-duplicate
// detection reads, so that is what it reports, as a count rather than a byte size. Reporting a
// fabricated byte total for sketches this package does not own would be worse than reporting the
// one number it can actually answer for.
const sketchSignatureKey = "signatures"

// Stats summarizes the store's current size and health.
//
// DedupRatio is the number Qompack.md §10's Phase 1 exit criterion measures — "store size vs. raw
// transcript ratio ≥ 4:1 on read-heavy sessions". It is RawBytes (every byte ever handed to Put,
// INCLUDING exact duplicates) over Bytes (compressed bytes actually written), which is what makes
// it a deduplication ratio rather than a compression ratio.
func (s *FSStore) Stats(ctx context.Context) (Stats, error) {
	if err := s.use(); err != nil {
		return Stats{}, err
	}
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}

	s.mu.RLock()
	st := Stats{
		Objects:  len(s.chunkSet),
		Bytes:    s.bytesOnDisk,
		RawBytes: s.rawBytes,
		ToolUses: len(s.toolUse),
		Files:    len(s.fileHist),
		Sketches: map[string]int{sketchSignatureKey: s.countSignaturesLocked()},
	}
	s.mu.RUnlock()

	if st.Bytes > 0 {
		st.DedupRatio = float64(st.RawBytes) / float64(st.Bytes)
	}
	st.Segments = s.segmentCount()
	return st, nil
}

// countSignaturesLocked counts roots carrying a MinHash signature. s.mu must be held.
func (s *FSStore) countSignaturesLocked() int {
	n := 0
	for _, e := range s.rootIndex {
		if e.Sig.Perms != 0 || len(e.Sig.Mins) > 0 {
			n++
		}
	}
	return n
}

// segmentCount returns how many segments the log holds. It takes the log's own mutex rather than
// the store's, because the segment log is an independently locked sub-object.
func (s *FSStore) segmentCount() int {
	if s.seg == nil {
		return 0
	}
	s.seg.mu.Lock()
	defer s.seg.mu.Unlock()
	return len(s.seg.byID)
}
