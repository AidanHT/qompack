package scheduler

// SkiRentalShouldWrite is Qompack.md §5.6 / Appendix A:
//
//	write when E[remaining reads] > w/r
//
// The threshold is COMPUTED from config, never written as a literal (00-ARCHITECTURE §11.6).
// Nothing in SP-12 calls this; SP-16 applies the policy. It lives here because
// 00-ARCHITECTURE §5.13 places the signature in this package.
//
// The "≈12.5" in §5.6 and in §5.13's signature comment is the FIVE-MINUTE figure and is not the
// only one. Cache writes are 1.25× base input at the 5-minute TTL and 2× at the 1-hour TTL, so
// w/r is 12.5 under one regime and 20 under the other — a caller that hardcodes either is
// wrong half the time. Pass w from CacheRegime.WriteMultiplier (see cacheregime.go), never
// from cfg.Cache.WriteMultiplier directly: the config key is Appendix C's 5-minute floor, and
// the regime is what the session is actually billed at.
//
// A non-positive r makes the ratio undefined, and a non-positive w describes a free write that
// cannot be free, so both conservatively report false.
func SkiRentalShouldWrite(expectedReads, r, w float64) bool {
	if r <= 0 || w <= 0 {
		return false
	}
	return expectedReads > w/r
}
