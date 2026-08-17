package contract_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
)

// sentinelTranscriptFixture is the golden transcript whose tail contains the rendered sentinel for
// MintSentinel("sess-fixture-01", 1767225500000), following the convention golden_test.go uses:
// a direct relative path from the package directory into testdata/golden/contracts/contract/.
const sentinelTranscriptFixture = "../../testdata/golden/contracts/contract/input/transcript_with_sentinel.jsonl"

// TestSentinelRoundTrip proves the mechanism end to end: mint, render (implicitly, since the golden
// fixture already contains a rendered sentinel for this exact sess/now pair), and scan.
func TestSentinelRoundTrip(t *testing.T) {
	sess := core.SessionID("sess-fixture-01")
	ts := core.UnixMilli(1767225500000)
	s := contract.MintSentinel(sess, ts)

	found, err := contract.ScanTranscriptTail(sentinelTranscriptFixture, s.Token, 64<<10)
	require.NoError(t, err)
	require.True(t, found, "the golden fixture's tail must contain the rendered sentinel for this token")
}

// TestSentinelNotObservedGivesTwoChances asserts the CAdditionalContext assertion's real Check
// reads History.Sentinel.Chances rather than tracking it itself (RecordSentinelScan does that, per
// the ruling that Chances tracking belongs to the daemon's scan): one spent chance still reports OK,
// a second spent chance fails at SevCritical.
func TestSentinelNotObservedGivesTwoChances(t *testing.T) {
	contract.DeclareProducer(contract.CAdditionalContext)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{}
	a := assertionByID(t, contract.CAdditionalContext)
	env := contract.Env{Clock: newFakeClock(), History: h}

	h.RecordSentinelScan(false)
	require.Equal(t, 1, h.Sentinel.Chances)
	r := a.Check(context.Background(), env)
	require.True(t, r.OK, "one spent chance is not yet a failure")

	h.RecordSentinelScan(false)
	require.Equal(t, 2, h.Sentinel.Chances)
	r = a.Check(context.Background(), env)
	require.False(t, r.OK, "two spent chances without ever observing the sentinel must fail")
	require.Equal(t, contract.SevCritical, r.Severity)
}

// TestSentinelRenderIsAnInertComment pins the exact rendered shape: an HTML comment, on its own
// line, with a 12-lowercase-hex-char token.
func TestSentinelRenderIsAnInertComment(t *testing.T) {
	s := contract.MintSentinel(core.SessionID("sess-x"), core.UnixMilli(42))

	require.Regexp(t, regexp.MustCompile(`^[a-f0-9]{12}$`), tokenSuffix(t, s.Token),
		"the token must be exactly 12 lowercase hex characters")

	rendered := contract.RenderSentinel(s)
	require.Equal(t, "<!-- qompack-contract-probe "+s.Token+" -->", rendered)
}

// tokenSuffix strips the "qompack-contract-" prefix from a minted token, failing the test if it is
// not present.
func tokenSuffix(t *testing.T, token string) string {
	t.Helper()
	const prefix = "qompack-contract-"
	require.True(t, len(token) > len(prefix) && token[:len(prefix)] == prefix, "token %q must start with %q", token, prefix)
	return token[len(prefix):]
}
