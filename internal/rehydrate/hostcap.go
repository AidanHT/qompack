package rehydrate

import (
	"unicode/utf16"

	"github.com/qompack/qompack/internal/core"
)

// The host ceiling (owner decision D5, 2026-09-22; Qompack.md v1.6 §8.6; ADR 0011 §21).
//
// Claude Code delivers a hook's additionalContext whole only up to 10,000 characters. Past that it
// saves the text to a file, hands the model the file path and a 2,000-character preview in its
// place, and "doesn't ask Claude to read the file" — so a rehydration that overruns the cap loses
// everything after its first 2,000 characters, drop report and retrieval line included, with no
// check failing anywhere. The cap "has no setting or environment variable to raise it"
// (testdata/host/hooks-output-schema.json, `limits`; internal/hookio.HostFieldMaxChars).
//
// So the rendered payload is bounded in HOST CHARACTERS as well as in estimator tokens, and the
// character bound is a host constant rather than configuration: no key may raise it, because
// raising it would only move the payload back behind the host's file-path fallback.
const (
	// HostContextCeilingChars bounds the WHOLE SessionStart(source=compact) additionalContext field
	// Qompack hands the host: the injection-tagged payload Build renders plus the one contract-probe
	// line the daemon's session.start route appends after the close tag. It sits 500 characters under
	// the host's 10,000, headroom for anything a future host prepends or counts differently.
	HostContextCeilingChars = 9500

	// probeReserveChars is the room Build leaves for the daemon's §12.1 contract-probe line
	// ("\n" + contract.RenderSentinel(...)), which is appended outside the injection span and so is
	// never part of Result.Text. The line is 62 characters for every session id — the token is a
	// fixed prefix plus twelve hex digits — and TestHostCeiling_ProbeReserveCoversTheProbeLine pins
	// the margin, because rehydrate may not import contract (00-ARCHITECTURE.md §3.2).
	probeReserveChars = 100

	// PayloadCeilingChars is the hard ceiling on Result.Text, in host characters: everything Build
	// renders, wrapper, headings, separators, handles and the overflow report included.
	PayloadCeilingChars = HostContextCeilingChars - probeReserveChars
)

// hostChars is s's length as Claude Code measures a hook field against its cap: UTF-16 code units,
// JavaScript's String.prototype.length on the parsed field. The hooks reference says "characters";
// the unit is read from the host's own code (2.1.280 tests `field.length <= 1e4`), recorded in
// plans/sdd/V6-closeout/rehydrate-cap/evidence/host-cap-unit.txt.
//
// It is the same count internal/hookio.HostChars makes, restated because rehydrate may not import
// hookio (§3.2); TestHostChars_AgreesWithTheHookClient ties the two. A rune outside the Basic
// Multilingual Plane is two units, everything else one — including the U+FFFD an invalid byte
// becomes, both here (range yields utf8.RuneError per bad byte) and on the wire (encoding/json
// writes one replacement character per bad byte). The count is never smaller than the rune count,
// so no reading of "characters" makes a payload this function passes longer than the ceiling.
func hostChars(s string) int {
	n := 0
	for _, r := range s {
		if l := utf16.RuneLen(r); l > 1 {
			n += l
			continue
		}
		n++
	}
	return n
}

// cost is a price in the two dimensions every admission must fit at once: estimator tokens against
// the token budget (Request.Budget, clamped by runtime.rehydrate.maxTokens) and host characters
// against PayloadCeilingChars.
//
// The character half is EXACT, not an estimate: a unit's text, a section's heading line and its
// blank-line separator, and the injection wrapper are all priced by hostChars on the very strings
// renderBody and Wrap concatenate, so the sum Build plans with is the length of the payload it
// renders. That is what lets the ceiling be a guarantee rather than a target.
type cost struct {
	tok   core.Tokens
	chars int
}

// plus returns c + o in both dimensions.
func (c cost) plus(o cost) cost { return cost{tok: c.tok + o.tok, chars: c.chars + o.chars} }

// minus returns c - o in both dimensions.
func (c cost) minus(o cost) cost { return cost{tok: c.tok - o.tok, chars: c.chars - o.chars} }

// within reports whether c fits limit in BOTH dimensions.
func (c cost) within(limit cost) bool { return c.tok <= limit.tok && c.chars <= limit.chars }

// atLeast returns the per-dimension maximum of c and o.
func (c cost) atLeast(o cost) cost {
	if o.tok > c.tok {
		c.tok = o.tok
	}
	if o.chars > c.chars {
		c.chars = o.chars
	}
	return c
}

// atMost returns the per-dimension minimum of c and o.
func (c cost) atMost(o cost) cost {
	if o.tok < c.tok {
		c.tok = o.tok
	}
	if o.chars < c.chars {
		c.chars = o.chars
	}
	return c
}

// nonNegative clamps each dimension of c at zero.
func (c cost) nonNegative() cost { return c.atLeast(cost{}) }

// unitCost is u's priced cost.
func unitCost(u unit) cost { return cost{tok: u.tokens, chars: u.chars} }

// sumCost is the priced cost of units, in order.
func sumCost(units []unit) cost {
	var c cost
	for _, u := range units {
		c = c.plus(unitCost(u))
	}
	return c
}

// sectionChars is what emitting kind k's section costs in host characters beyond its units: the
// "\n" renderBody's join puts before it, the heading line, and the heading's own newline.
//
// Item 3's heading carries the builder's "(top N of M …)" counts. Those are fixed at build time
// (eliminationsHeading documents why), so the exact rendered heading is priced here rather than a
// widest-case sample: the character accounting is exact or it is not a guarantee.
func sectionChars(k ItemKind, b built) int {
	h := sectionHeading(k)
	if k == ItemEliminations {
		h = eliminationsHeading(eliminationsShown(b), b.seen)
	}
	if l := sectionLegend(k, b); l != "" {
		h += "\n" + l
	}
	return 1 + hostChars(h) + 1
}

// sectionCost is sectionChars plus the token price headingCost already charges for the same lines.
func sectionCost(d Deps, k ItemKind, b built) cost {
	return cost{tok: headingCost(d, k, b), chars: sectionChars(k, b)}
}

// wrapperChars is the injection wrapper's exact host-character cost: the open tag, the document
// header and the close tag, plus the two newlines Wrap and renderBody put around them. Every
// section's own separator is charged to that section (sectionChars), so a payload's length is
// wrapperChars + Σ over emitted sections of (sectionChars + Σ unit chars).
func wrapperChars(openTag, header, closeTag string) int {
	return hostChars(openTag) + hostChars(header) + hostChars(closeTag) + 2
}
