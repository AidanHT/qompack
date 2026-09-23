package negknow

// SP09-D1 (plans/V2-WAVE1-carried-defects.md), V6 close-out. TestBudget_Open grades Open on the
// whole process's CPU, garbage collection included, so every allocation Open makes per record is
// charged twice: once to make it and again to collect it. Replaying the 20 000-record benchmark log
// made about twenty allocations per record, and three sources of them named nothing the ledger
// keeps: a decode buffer and, on failure, an error for every hash field core.ParseHash parsed; the
// 64-character hex spelling of both index keys of every record; and a fresh logLine per line.
//
// These tests pin the first two by allocation count, which co-load cannot inflate (ADR 0010), and
// hold the local hash parser to core.ParseHash's answers. The third is evidenced by the benchmark's
// allocs/op in the V6 close-out report.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
)

// TestParseHash_AgreesWithCore holds the replay's hash parser to core.ParseHash: the same digest
// for every string core accepts, and a refusal for every string core refuses.
func TestParseHash_AgreesWithCore(t *testing.T) {
	valid := testEvidence("parse").String()
	bare := strings.TrimPrefix(valid, "sha256:")
	cases := []string{
		"", valid, bare, strings.ToUpper(bare), "sha256:" + strings.ToUpper(bare), "SHA256:" + bare,
		"sha256:sha256:" + bare, bare[:63], bare + "0", "sha256:" + bare[:62] + "g0", " " + bare,
		bare[:10] + "Aa" + bare[12:], "sha256:", "sha256", "0x" + bare[2:], bare[:63] + "\x00",
	}
	check := func(tb require.TestingT, s string) {
		want, err := core.ParseHash(s)
		got, ok := parseHash(s)
		require.Equal(tb, err == nil, ok, "%q: core accepts=%v", s, err == nil)
		require.Equal(tb, want, got, "%q", s)
		require.Equal(tb, want, parseHashOrZero(s), "%q", s)
	}
	for _, s := range cases {
		check(t, s)
	}
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.StringMatching(`(sha256:)?[0-9a-fA-FgxG:]{60,68}`).Draw(rt, "s")
		check(rt, s)
	})
}

// TestParseHashOrZero_DoesNotAllocate: parsing a record's hash fields — valid, absent or damaged —
// allocates nothing.
func TestParseHashOrZero_DoesNotAllocate(t *testing.T) {
	valid := testEvidence("alloc").String()
	for _, s := range []string{valid, strings.TrimPrefix(valid, "sha256:"), "", "sha256:zz", valid[:40]} {
		allocs := testing.AllocsPerRun(20, func() { _ = parseHashOrZero(s) })
		require.Zero(t, allocs, "parseHashOrZero(%q) allocated %.0f times", s, allocs)
	}
}

// TestReindex_AllocatesNoPerRecordKeys: rebuilding byMatch and byKey allocates the maps, the key
// slice and one index list per DISTINCT match key — and nothing per record. The hex-keyed form
// allocated two strings per record on top of that.
func TestReindex_AllocatesNoPerRecordKeys(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	const records = 2000
	seedActive(t, l, records, nil)
	distinct := map[core.Hash]bool{}
	for i := range l.recs {
		distinct[l.recs[i].Desc.matchHash()] = true
	}
	require.Len(t, distinct, records, "every seeded record has its own target, so its own match key")

	allocs := testing.AllocsPerRun(3, func() { _ = l.reindex() })
	require.LessOrEqual(t, allocs, float64(len(distinct)+32),
		"reindex over %d records with %d distinct match keys allocated %.0f times", records, len(distinct), allocs)
}
