package canon

import "github.com/qompack/qompack/internal/config"

// Default returns the Registry a project gets when it has not configured its own set.
//
// It registers nothing in this commit, because nothing exists to register: the seven generic
// canonicalizers land in the next commit and the seven per-tool ones in the one after, and this
// grows to the full fourteen there. What it already does is return a REAL registry rather than
// SP-01's stub, which is the part that matters now — Run composes, Restore inverts, and the
// conformance suite's behaviour block stops being skipped, so every property the following two
// commits add rules to is already being asserted about the machinery those rules plug into.
//
// cfg is accepted and ignored for the same reason: the signature is §5.6's and may not change, and
// the Enabled/Strip handling belongs with the canonicalizers it selects between.
func Default(cfg config.CanonicalizeCfg) Registry {
	return NewRegistry()
}
