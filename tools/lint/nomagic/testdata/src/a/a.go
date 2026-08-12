// Package a is the fixture package for TestNoMagic_Analyzer (see ../../nomagic_test.go). It
// exists only to be loaded by golang.org/x/tools/go/analysis/analysistest and is not part of any
// real Qompack package.
package a

// forbiddenIntLiteral duplicates a config default (scheduler.hardCeilingMargin, Appendix C) and
// must be reported.
const forbiddenIntLiteral = 20000 // want `literal 20000 duplicates a config default; read it from config \(D11, §11\.6\)`

// forbiddenFloatLiteral duplicates a config default (scheduler.cache.readMultiplier, Appendix C)
// and must be reported.
const forbiddenFloatLiteral = 0.1 // want `literal 0\.1 duplicates a config default; read it from config \(D11, §11\.6\)`

// allowedIntLiteral duplicates the same config default as forbiddenIntLiteral, but the
// //nomagic:allow comment with a reason exempts this exact line.
const allowedIntLiteral = 20000 //nomagic:allow test fixture value, exercises the allow-comment escape hatch

// ordinaryLiteral is not a member of either forbidden set and must never be reported.
const ordinaryLiteral = 7

// alternateSpellingTrailingZero and alternateSpellingExponent are the same real number as
// forbiddenFloatLiteral (0.1) spelled differently. Comparison is on the parsed value, not the
// source text, so both must be reported too.
const alternateSpellingTrailingZero = 0.10 // want `literal 0\.10 duplicates a config default; read it from config \(D11, §11\.6\)`

const alternateSpellingExponent = 1e-1 // want `literal 1e-1 duplicates a config default; read it from config \(D11, §11\.6\)`
