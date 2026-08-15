package eval

import "github.com/qompack/qompack/internal/core"

// Constants of the system Qompack surrounds (§2.3, §2.4, §2.5).
//
// They are NOT Qompack tunables and must never be moved into internal/config: changing them would
// change what "stock behaviour" means, which is precisely what the Phase 0 baseline measures. D11
// governs Qompack's own knobs; modelling the host faithfully requires naming the host's numbers,
// which is what the //nomagic:allow annotations below say.
const (
	// hostTopFiles is §2.4 step 7's restore fan-out: the top 5 recently-read files.
	hostTopFiles = 5
	// hostPerFileTokens is §2.4 step 7's per-file cap.
	hostPerFileTokens core.Tokens = 5000
	// hostRestoreBudget is §2.4 step 7's total file-restore budget.
	hostRestoreBudget core.Tokens = 50000
	// hostSkillBudget is §2.4 step 7's invoked-skills budget.
	hostSkillBudget core.Tokens = 25000
	// hostPreserveMinTokens is §2.3's minTokens.
	hostPreserveMinTokens core.Tokens = 10000 //nomagic:allow §2.3 minTokens — a host constant, not a Qompack tunable
	// hostPreserveMinMessages is §2.3's minTextBlockMessages.
	hostPreserveMinMessages = 5
	// hostPreserveMaxTokens is §2.3's maxTokens, and the cap the expansion runs under.
	hostPreserveMaxTokens core.Tokens = 40000
	// hostMaxOutputReserve is §2.5's min(maxOutputTokens, 20_000).
	hostMaxOutputReserve core.Tokens = 20000 //nomagic:allow §2.5 output reserve — a host constant, not a Qompack tunable
	// hostAutoCompactBuffer is §2.5's effectiveWindow − 13_000.
	hostAutoCompactBuffer core.Tokens = 13000
	// hostTokenPadNumerator and hostTokenPadDenominator are §2.2's 4/3 padding.
	hostTokenPadNumerator   = 4
	hostTokenPadDenominator = 3
)

// hostEffectiveContextWindow is §2.5's first line:
//
//	effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
func hostEffectiveContextWindow(contextWindow core.Tokens) core.Tokens {
	return contextWindow - hostMaxOutputReserve
}

// hostAutoCompactThreshold is §2.5's second line:
//
//	autoCompactThreshold = effectiveContextWindow − 13_000
//
// For a 200K model it yields 167_000 — the residual span at which stock compacts, and the anchor
// the latency model is calibrated against.
func hostAutoCompactThreshold(contextWindow core.Tokens) core.Tokens {
	return hostEffectiveContextWindow(contextWindow) - hostAutoCompactBuffer
}

// hostPadTokens is §2.2's 4/3 padding: the coarseness SP-06 later fixes, reproduced here rather
// than improved on, because the stock number has to describe the host as it is.
func hostPadTokens(t core.Tokens) core.Tokens {
	return t * hostTokenPadNumerator / hostTokenPadDenominator
}
