package tokens

import (
	"context"
	"math"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Estimator prices content in tokens. Estimate/EstimateString/EstimateRoot are pure given the
// current Factor; Calibrate is the only method that mutates state.
type Estimator interface {
	Estimate(b []byte, c Class) core.Tokens
	EstimateString(s string, c Class) core.Tokens
	// EstimateRoot uses per-chunk cached measurements keyed by chunk hash — the "exact chunk-level
	// accounting" G10.2 asks for — so it never re-scans bytes already accounted for. It takes
	// []core.ChunkRef, not a store type, because tokens must not import store (§3.2).
	EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c Class) core.Tokens
	// Calibrate nudges Factor toward observed/estimated by one exponential-moving-average step and
	// persists the result. Calls where estimated == 0 are ignored.
	Calibrate(observed core.Tokens, estimated core.Tokens)
	Factor() float64
}

// charsPerToken returns cfg's characters-per-token constant for c. It prices a whole payload by
// byte length, which is what the exact estimator falls back to for a chunk it has never measured
// (EstimateRoot's cache-miss path). Image and PDF are priced by dimensions/pages, not characters,
// so both fall back to the binary rate: a CHUNK of an image or a PDF is opaque bytes, and pricing
// it consistently with other opaque content is the only honest answer available without the whole
// file in hand.
func charsPerToken(cfg config.RTokensCfg, c Class) float64 {
	switch c {
	case ClassProse:
		return cfg.ProseCharsPerToken
	case ClassCode:
		return cfg.CodeCharsPerToken
	case ClassJSON:
		return cfg.JSONCharsPerToken
	case ClassDiff:
		return cfg.DiffCharsPerToken
	case ClassImage, ClassPDF, ClassBinary:
		return cfg.BinaryCharsPerToken
	default:
		return cfg.BinaryCharsPerToken
	}
}

// ceilDivBytes returns ceil(n / per), treating per <= 0 as "one token per byte" rather than
// dividing by zero.
func ceilDivBytes(n int, per float64) int {
	if per <= 0 {
		return n
	}
	return int(math.Ceil(float64(n) / per))
}
