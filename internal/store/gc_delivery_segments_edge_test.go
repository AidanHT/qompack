package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Edge rows for the read-only delivery-segment decoder (SP20-D4) that the literal-fixture GC tests in
// gc_delivery_segments_test.go do not reach: each malformed authority, name or seal shape the decoder
// refuses, and the two shapes it accepts that no GC fixture builds (an empty log with no head, and a
// v2 seal whose two slots are both valid). Every refusal is errRetentionRootsUnavailable, the halt
// that keeps a pass from sweeping against authority it could not read (w16b-cover, C3.6).

// requireRetentionHalt asserts err is the retention halt and carries the given reason.
func requireRetentionHalt(t *testing.T, err error, reason string) {
	t.Helper()
	require.ErrorIs(t, err, errRetentionRootsUnavailable)
	require.Contains(t, err.Error(), reason)
}

// TestDsegParseSeqName_AcceptsOnlyACanonicalNonZeroSequence: a segment directory is named by exactly
// its 20-digit zero-padded sequence, and segment 0 never lives under delivery-segments.
func TestDsegParseSeqName_AcceptsOnlyACanonicalNonZeroSequence(t *testing.T) {
	seq, ok := dsegParseSeqName("00000000000000000007")
	require.True(t, ok)
	require.Equal(t, uint64(7), seq)

	for name, why := range map[string]string{
		"7":                     "short",
		"000000000000000000007": "long",
		"0000000000000000000x":  "not a digit",
		"00000000000000000000":  "segment 0 never lives here",
		"99999999999999999999":  "past uint64",
	} {
		_, ok := dsegParseSeqName(name)
		require.False(t, ok, why)
	}
}

// TestDsegDecodeTransition_AcceptsOnlyOneCanonicalRecordLine: a transition is one canonical,
// current-version record with a hex chain, ending in exactly one newline.
func TestDsegDecodeTransition_AcceptsOnlyOneCanonicalRecordLine(t *testing.T) {
	chain := dsegChain(dsegChainSeed, 0, 0, "")
	good, err := json.Marshal(dsegTransition{Version: dsegVersion, Chain: hex.EncodeToString(chain[:])})
	require.NoError(t, err)
	rec, got, ok := dsegDecodeTransition(append(append([]byte(nil), good...), '\n'))
	require.True(t, ok)
	require.Equal(t, chain, got)
	require.Equal(t, int64(0), rec.Seq)

	for why, line := range map[string]string{
		"not json":         "nope\n",
		"unknown version":  strings.Replace(string(good), `"v":1`, `"v":2`, 1) + "\n",
		"noncanonical":     strings.Replace(string(good), `{"v":1,`, `{"v":1, `, 1) + "\n",
		"no newline":       string(good),
		"chain is not hex": `{"v":1,"seq":0,"active":0,"base_root":"","chain":"zz"}` + "\n",
	} {
		_, _, ok := dsegDecodeTransition([]byte(line))
		require.False(t, ok, why)
	}
}

// TestDsegValidateLog_RefusesAnOversizedBadlyChainedOrEmptyLog covers the log's own refusals beside
// the torn, non-dense and malformed rows the GC fixtures already reach.
func TestDsegValidateLog_RefusesAnOversizedBadlyChainedOrEmptyLog(t *testing.T) {
	_, _, err := dsegValidateLog(nil)
	requireRetentionHalt(t, err, "log is empty")

	_, _, err = dsegValidateLog(append(bytes.Repeat([]byte{'x'}, dsegMaxLine), '\n'))
	requireRetentionHalt(t, err, "exceeds")

	_, _, err = dsegValidateLog([]byte("{\"v\":1}\n"))
	requireRetentionHalt(t, err, "malformed or out of sequence")

	// A dense, canonical record whose chain is not the fold of its predecessor.
	wrong := dsegChain(dsegChainSeed, 9, 0, "")
	line, jerr := json.Marshal(dsegTransition{Version: dsegVersion, Chain: hex.EncodeToString(wrong[:])})
	require.NoError(t, jerr)
	_, _, err = dsegValidateLog(append(line, '\n'))
	requireRetentionHalt(t, err, "chain/base root invalid")
}

// writeStateFile writes name under the project's state directory.
func writeStateFile(t *testing.T, tp *testProject, name string, b []byte) {
	t.Helper()
	p := filepath.Join(paths.Of(tp.Root).State, name)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), b, 0o600))
}

// TestResolveDeliverySegments_AnEmptyLogWithoutAHeadIsLegacyOnlyWithoutEvidence: an empty log and no
// head is not authority. On an unmigrated tree it is the legacy segment alone; once the tree carries
// migration evidence it is head loss, and the pass halts.
func TestResolveDeliverySegments_AnEmptyLogWithoutAHeadIsLegacyOnlyWithoutEvidence(t *testing.T) {
	tp := newTestStore(t)
	writeStateFile(t, tp, dsegLogFile, nil)

	view, err := tp.Store.resolveDeliverySegments(nil)
	require.NoError(t, err)
	require.True(t, view.legacy)
	require.Equal(t, []uint64{0}, view.committed)

	require.NoError(t, os.Mkdir(paths.Long(filepath.Join(paths.Of(tp.Root).State, dsegGensDir)), 0o700))
	_, err = tp.Store.resolveDeliverySegments(nil)
	requireRetentionHalt(t, err, "log empty, no head, migration evidence present")
}

// TestResolveDeliverySegments_HaltsOnAHalfPresentOrInconsistentAuthority: a log without a head is head
// loss, a head without a log is log loss, a head naming a record past the log's end is ahead of it, a
// head that disagrees with the record it names is a conflict, and an authority file past its bound is
// unreadable. None of them may fall back to the legacy segment.
func TestResolveDeliverySegments_HaltsOnAHalfPresentOrInconsistentAuthority(t *testing.T) {
	logB, headB := buildSegAuthority(t, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	var head dsegHead
	require.NoError(t, json.Unmarshal(headB, &head))
	marshalHead := func(h dsegHead) []byte {
		b, err := json.Marshal(h)
		require.NoError(t, err)
		return b
	}
	ahead := head
	ahead.Seq = 5
	conflict := head
	conflict.LastLen--

	cases := []struct {
		name   string
		files  map[string][]byte
		reason string
	}{
		{"log without head", map[string][]byte{dsegLogFile: logB}, "without a head"},
		{"head without log", map[string][]byte{dsegHeadFile: headB}, "without a log"},
		{"head ahead of log", map[string][]byte{dsegLogFile: logB, dsegHeadFile: marshalHead(ahead)}, "ahead of the log"},
		{"head conflicts", map[string][]byte{dsegLogFile: logB, dsegHeadFile: marshalHead(conflict)}, "conflicts with the record"},
		{"oversized log", map[string][]byte{dsegLogFile: bytes.Repeat([]byte{'\n'}, dsegMaxLog+1)}, "byte bound"},
		{
			"noncanonical head",
			map[string][]byte{dsegLogFile: logB, dsegHeadFile: append([]byte(" "), headB...)},
			"noncanonical bytes",
		},
		{
			"head chain not hex",
			map[string][]byte{dsegLogFile: logB, dsegHeadFile: bytes.Replace(headB, []byte(head.Chain), []byte(strings.Repeat("z", 64)), 1)},
			"chain/base root invalid",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp := newTestStore(t)
			for name, b := range c.files {
				writeStateFile(t, tp, name, b)
			}
			_, err := tp.Store.resolveDeliverySegments(nil)
			requireRetentionHalt(t, err, c.reason)
		})
	}
}

// TestResolveDeliverySegments_HaltsOnAMalformedCommittedSegment: a committed segment must hold its two
// journals as regular files and both seals non-empty; an ack journal that is a directory, or an empty
// seal, halts the pass.
func TestResolveDeliverySegments_HaltsOnAMalformedCommittedSegment(t *testing.T) {
	specs := []segSpec{{0, ""}, {1, segBaseRoot("seg1")}}

	t.Run("ack journal is a directory", func(t *testing.T) {
		tp := newTestStore(t)
		installSegAuthority(t, tp, specs)
		ack := filepath.Join(segDirOf(tp, 1), deliveryAckFile)
		require.NoError(t, os.Remove(paths.Long(ack)))
		require.NoError(t, os.Mkdir(paths.Long(ack), 0o700))
		_, err := tp.Store.resolveDeliverySegments(nil)
		requireRetentionHalt(t, err, "not a regular file")
	})
	t.Run("empty seal", func(t *testing.T) {
		tp := newTestStore(t)
		installSegAuthority(t, tp, specs)
		seal := filepath.Join(segDirOf(tp, 1), deliveryAckPositionFile)
		require.NoError(t, os.WriteFile(paths.Long(seal), nil, 0o600))
		_, err := tp.Store.resolveDeliverySegments(nil)
		requireRetentionHalt(t, err, "is empty")
	})
}

// TestDsegListSegmentDirs_HaltsOnAnythingButCanonicalSegmentDirectories: delivery-segments holds only
// canonically named segment directories. A stray name, a canonically named regular file, or a
// delivery-segments that is itself a file halts — whether it is met as migration evidence (no
// authority yet) or while listing the staged segments of a committed authority.
func TestDsegListSegmentDirs_HaltsOnAnythingButCanonicalSegmentDirectories(t *testing.T) {
	segDir := func(tp *testProject) string { return filepath.Join(paths.Of(tp.Root).State, dsegDir) }

	t.Run("stray name, as evidence", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(segDir(tp), "notes")), 0o700))
		_, err := tp.Store.resolveDeliverySegments(nil)
		requireRetentionHalt(t, err, "non-canonical entry")
	})
	t.Run("canonical name on a file, as evidence", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, os.MkdirAll(paths.Long(segDir(tp)), 0o700))
		f := filepath.Join(segDir(tp), "00000000000000000001")
		require.NoError(t, os.WriteFile(paths.Long(f), nil, 0o600))
		_, err := tp.Store.resolveDeliverySegments(nil)
		requireRetentionHalt(t, err, "not a plain directory")
	})
	t.Run("generations path is a file, as evidence", func(t *testing.T) {
		tp := newTestStore(t)
		writeStateFile(t, tp, dsegGensDir, nil)
		_, err := tp.Store.resolveDeliverySegments(nil)
		requireRetentionHalt(t, err, "is not a directory")
	})
	t.Run("delivery-segments is a file, while listing staged segments", func(t *testing.T) {
		tp := newTestStore(t)
		installSegAuthority(t, tp, []segSpec{{0, ""}})
		writeStateFile(t, tp, dsegDir, nil)
		_, err := tp.Store.resolveDeliverySegments(nil)
		requireRetentionHalt(t, err, "is not a directory")
	})
}

// sealRegion pads body to one slot region.
func sealRegion(body []byte) []byte {
	region := bytes.Repeat([]byte{dsealPad}, dsealSlotRegion)
	copy(region, body)
	return region
}

// sealRecordBody renders a correctly summed, in-bounds record for slot.
func sealRecordBody(t *testing.T, slot byte, seq uint64, size int64, count int) []byte {
	t.Helper()
	chain := core.HashBytes("test.seal.chain", []byte{slot, byte(seq)})
	rec := dsealRecord{Seq: seq, Bytes: size, Count: count, Chain: chain}
	rec.Sum = dsealSum(dsealAckChainDomain, slot, rec.Seq, rec.Bytes, rec.Count, rec.Chain)
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	return b
}

// TestDsealClassifySlot_RefusesAWrongSizeUnpaddedOrUnparseableRegion: a slot region is exactly its
// fixed size, a body and then only padding; anything else is invalid, never empty.
func TestDsealClassifySlot_RefusesAWrongSizeUnpaddedOrUnparseableRegion(t *testing.T) {
	valid := sealRecordBody(t, 'a', 1, 5, 1)
	_, st := dsealClassifySlot(sealRegion(valid), 'a', dsealAckChainDomain, dsealAckSeed)
	require.Equal(t, dsealSlotValid, st)

	for why, region := range map[string][]byte{
		"wrong size":         valid,
		"no padding at all":  bytes.Repeat([]byte{'x'}, dsealSlotRegion),
		"junk after padding": append(sealRegion(nil)[:dsealSlotRegion-1], 'x'),
		"unparseable body":   sealRegion([]byte(`{"seq":`)),
	} {
		_, st := dsealClassifySlot(region, 'a', dsealAckChainDomain, dsealAckSeed)
		require.Equal(t, dsealSlotInvalid, st, why)
	}
}

// TestDsealSelect_AcceptsTwoValidSlotsOneSequenceApart: the running producer's steady state is both
// slots valid, the newer one sequence ahead and sealing strictly more bytes and entries — in either
// slot. A pair that is not one sequence apart, or a buffer that is not a seal image, refuses.
func TestDsealSelect_AcceptsTwoValidSlotsOneSequenceApart(t *testing.T) {
	image := func(a, b []byte) []byte {
		img := bytes.Repeat([]byte{dsealPad}, dsealFileSize)
		copy(img, dsealPrefixA)
		copy(img[dsealStride:], dsealPrefixB)
		copy(img[dsealFileSize-len(dsealSuffix):], dsealSuffix)
		copy(img[dsealSlotAOffset:], sealRegion(a))
		copy(img[dsealSlotBOffset:], sealRegion(b))
		return img
	}
	newerInB := image(sealRecordBody(t, 'a', 1, 5, 1), sealRecordBody(t, 'b', 2, 9, 2))
	require.True(t, dsealSelect(newerInB, dsealAckChainDomain, dsealAckSeed))
	newerInA := image(sealRecordBody(t, 'a', 3, 12, 3), sealRecordBody(t, 'b', 2, 9, 2))
	require.True(t, dsealSelect(newerInA, dsealAckChainDomain, dsealAckSeed))

	apart := image(sealRecordBody(t, 'a', 1, 5, 1), sealRecordBody(t, 'b', 4, 9, 2))
	require.False(t, dsealSelect(apart, dsealAckChainDomain, dsealAckSeed), "slots two sequences apart")
	require.False(t, dsealSelect(newerInB[:dsealFileSize-1], dsealAckChainDomain, dsealAckSeed), "not an image")
}

// TestDsealParsers_RefuseAnOversizedDocument: the v1 sidecar and the frozen segment-0 seal are both
// bounded documents; one past the bound is refused unread.
func TestDsealParsers_RefuseAnOversizedDocument(t *testing.T) {
	big := bytes.Repeat([]byte{' '}, dsealMaxLine+1)
	require.False(t, dsealParseV1(big, dsealAckSeed))
	require.False(t, dsealParseFrozen(big, dsealAckSeed))
}
