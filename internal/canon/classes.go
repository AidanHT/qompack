package canon

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/sketch"
)

// MinHashOptions is sketch.MinHashOptions under canon's own name, so callers configuring a
// canonicalization pass need not import sketch for the one field. It is an alias, not a defined
// type: Options.MinHash is a sketch.MinHashOptions and the two must stay assignable.
type MinHashOptions = sketch.MinHashOptions

// DefaultShingleSize is the shingle length OptionsFrom asks sketch.MinHash for. Appendix C's
// store.canonicalize.minhash block has no shingle key — it configures only enabled, permutations
// and nearDupThreshold — so the value is chosen here rather than read from config. Five tokens is
// the usual near-duplicate-detection shingle width: short enough that a one-line change to a test
// log perturbs only a handful of shingles, long enough that unrelated log lines do not collide.
const DefaultShingleSize = 5

// knownClasses is every Class, in the registration order 00-ARCHITECTURE.md §5.6 gives Default:
// the structural and generic classes first, then the per-tool canonicalizers reuse them.
var knownClasses = []Class{
	ClassCRLF,
	ClassANSI,
	ClassTimestamps,
	ClassDurations,
	ClassPIDs,
	ClassAddresses,
	ClassTmpPaths,
	ClassPaths,
}

// alwaysOn is the set of Classes applied regardless of Options.Strip.
//
// crlf and paths are structural, not cosmetic. 00-ARCHITECTURE.md §4 makes CRLF→LF normalization
// a precondition of cross-platform dedup — without it a Windows and a Linux read of the same file
// produce disjoint chunk sets — and separator normalization is the same argument applied to path
// text. Neither is one of Appendix C's six store.canonicalize.strip values, so neither can be
// turned off there either; making them opt-in would let a config fork the dedup space in half.
var alwaysOn = map[Class]bool{ClassCRLF: true, ClassPaths: true}

// KnownClasses returns every Class in registration order. The returned slice is a fresh copy, so
// a caller cannot mutate the package's own table.
func KnownClasses() []Class {
	out := make([]Class, len(knownClasses))
	copy(out, knownClasses)
	return out
}

// ParseClass resolves s to a Class. It is case-sensitive and matches Appendix C's spellings
// exactly — note tmpPaths is camelCase — because those strings are a wire format: they appear
// verbatim in every project's .qompack config and in internal/config's own strip enum.
func ParseClass(s string) (Class, bool) {
	for _, c := range knownClasses {
		if string(c) == s {
			return c, true
		}
	}
	return "", false
}

// gateSet returns the set of Classes in play for strip.
//
// A nil Strip means "every class" — the documented meaning of the zero Options (see Options.Strip)
// and what a caller who has not thought about classes should get. A non-nil Strip, INCLUDING an
// empty one, means exactly those classes; that distinction is what lets a test ask for "nothing
// optional" without also asking for "nothing at all". Either way the always-on structural classes
// are added, since neither can be disabled through Appendix C in the first place.
func gateSet(strip []Class) map[Class]bool {
	gate := make(map[Class]bool, len(knownClasses))
	if strip == nil {
		for _, c := range knownClasses {
			gate[c] = true
		}
		return gate
	}
	for _, c := range strip {
		gate[c] = true
	}
	for c := range alwaysOn {
		gate[c] = true
	}
	return gate
}

// OptionsFrom builds Options from Appendix C's store.canonicalize block.
//
// Unknown entries in cfg.Strip are skipped rather than rejected: internal/config's own Validate
// already reports store.canonicalize.strip values outside the enum, with the config path and the
// offending value, which is a far better diagnostic than anything this function could raise from
// a []Class it was handed. Registry.Run still rejects an unknown Class handed to it directly —
// that path has no config validation in front of it.
func OptionsFrom(cfg config.CanonicalizeCfg, keepDeltas bool) Options {
	strip := make([]Class, 0, len(cfg.Strip))
	for _, s := range cfg.Strip {
		if c, ok := ParseClass(s); ok {
			strip = append(strip, c)
		}
	}
	return Options{
		Strip:      strip,
		KeepDeltas: keepDeltas,
		MinHash: MinHashOptions{
			Enabled:          cfg.MinHash.Enabled,
			Permutations:     cfg.MinHash.Permutations,
			ShingleSize:      DefaultShingleSize,
			NearDupThreshold: cfg.MinHash.NearDupThreshold,
		},
	}
}
