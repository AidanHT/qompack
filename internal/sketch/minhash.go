package sketch

import "github.com/qompack/qompack/internal/core"

// MinHashOptions configures MinHash signature computation (00-ARCHITECTURE.md §5.7,
// Qompack.md §8.1).
type MinHashOptions struct {
	// Enabled turns near-duplicate detection on or off.
	Enabled bool
	// Permutations is the number of MinHash permutation functions.
	Permutations int
	// ShingleSize is the shingle length, in tokens, MinHash hashes over.
	ShingleSize int
	// NearDupThreshold is the Jaccard similarity above which two signatures are treated as
	// near-duplicates.
	NearDupThreshold float64
}

// Signature is a MinHash signature over canonicalized content, embedded in canon.Result to
// support near-duplicate detection.
type Signature struct {
	// Perms is the number of permutation functions Mins was computed with.
	Perms uint16
	// Mins holds one minimum hash value per permutation.
	Mins []uint64
}

// MinHash always returns the zero Signature. MinHash has no error return, so the empty
// signature — no permutations computed, no minimum hashes recorded — is Rule 1's documented zero
// value until SP-03 lands the real k-permutation minhashing algorithm.
func MinHash(data []byte, o MinHashOptions) Signature { return Signature{} }

// Jaccard always returns 0: with no minimum hashes recorded on either side, the stub has no
// grounds to claim any similarity — 0, not a divide-by-zero panic or a fabricated 1, is the
// honest answer.
func (s Signature) Jaccard(o Signature) float64 { return 0 }

// IsNearDup always reports false: claiming a near-duplicate would be a false positive that could
// cause a genuinely novel result to be skipped rather than stored — the same false-positive
// concern documented on Bloom.Test.
func (s Signature) IsNearDup(o Signature, threshold float64) bool { return false }

// MarshalBinary always reports core.ErrNotImplemented.
func (s Signature) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }

// UnmarshalBinary always reports core.ErrNotImplemented.
func (s *Signature) UnmarshalBinary(data []byte) error { return core.ErrNotImplemented }
