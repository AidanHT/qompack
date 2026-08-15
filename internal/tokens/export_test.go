package tokens

// This file exports package-internal helpers to the external tokens_test package, which is where
// every test in this package lives. It is a _test.go file, so nothing here ships in a binary.

// Units exposes the unit scanner for direct testing. The scanner is the whole basis of the exact
// estimator (G10.2), so its determinism is worth asserting on its own rather than only through
// Estimate's rounding and calibration.
func Units(b []byte) int { return units(b) }

// UnitWeight exposes one class's unit weight, so a test can state an expected token count as
// round(units * weight) rather than restating the weight as a literal.
func UnitWeight(c Class) float64 { return unitWeight[c] }

// ImageLongEdgeClamp exposes the host's documented long-edge pixel clamp.
func ImageLongEdgeClamp() int { return imageLongEdgeClamp }

// ChunkCacheHeaderMagic and ChunkCacheRecordSize expose the on-disk chunk-token cache's framing so
// a test can assert the file layout without duplicating the constants.
func ChunkCacheHeaderMagic() []byte { return []byte(chunkCacheMagic) }

// ChunkCacheRecordSize is the on-disk size of one chunk-token cache record.
func ChunkCacheRecordSize() int { return chunkCacheRecordSize }

// ChunkCacheHeaderSize is the on-disk size of the chunk-token cache header.
func ChunkCacheHeaderSize() int { return chunkCacheHeaderSize }

// CalibKeyForTest exposes the calibration key derivation, so a test can hand-write a calibration
// document under exactly the key the loader will look for.
func CalibKeyForTest(scope string) string { return calibKey(scope) }

// SetChunkCacheMaxEntries lowers the in-memory cache cap for the duration of one test and returns
// a function restoring it, so eviction and file compaction can be exercised without writing a
// quarter of a million records.
func SetChunkCacheMaxEntries(n int) func() {
	prev := chunkCacheMaxEntries
	chunkCacheMaxEntries = n
	return func() { chunkCacheMaxEntries = prev }
}

// CachedEntries reports how many measurements e currently holds in memory.
func CachedEntries(e Estimator) int {
	ex, ok := e.(*exact)
	if !ok {
		return -1
	}
	ex.cmu.Lock()
	defer ex.cmu.Unlock()
	return len(ex.cache)
}
