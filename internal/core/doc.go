// Package core holds the cross-cutting primitives every other Qompack package shares: the
// domain-separated Hash, the scalar identity types, the Clock seam, and the sentinel errors.
//
// core imports nothing from internal/ (§3.2). That is what lets every other package depend on
// it without creating a cycle, and it is why ChunkRef and Dep live here rather than in store or
// negknow: tokens must be able to size a root without importing store, and store must be able to
// answer staleness without importing negknow.
package core
