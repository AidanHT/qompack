package core_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestHashBytes_DomainSeparation(t *testing.T) {
	// The 0x00 separator is what makes ("a","bc") and ("ab","c") distinct. Without it both
	// would hash the concatenation "abc" and two domains would collide.
	a := core.HashBytes("a", []byte("bc"))
	b := core.HashBytes("ab", []byte("c"))
	require.NotEqual(t, a, b, "domain separator must prevent concatenation collisions")
}

func TestHashBytes_KnownVector(t *testing.T) {
	want := sha256.Sum256([]byte("qompack.root.v1\x00"))
	got := core.HashBytes("qompack.root.v1", nil)
	require.Equal(t, core.Hash(want), got)
}

// sinkHash keeps the digest below live so the compiler cannot delete the call being measured.
// testing.AllocsPerRun carries no equivalent of testing.B.Loop's "the loop body is not optimised
// away" guarantee, and an eliminated call would report zero allocations and pass vacuously.
var sinkHash core.Hash

func TestHashBytes_DoesNotAllocate(t *testing.T) {
	// HashBytes is on the L0 PostToolUse hot path (§8.1 item 5) and runs 5 000 times in one Bloom
	// rebuild, so an allocation here is multiplied by every caller in the tree. It cost exactly one
	// 32-byte allocation per call until HashBytes stopped routing the digest through h.Sum(nil),
	// and a nanosecond count hid it: the sketch benchmarks met their latency budgets throughout.
	// This assertion is the durable guard, because the fix is a one-line detail a later refactor
	// would revert without noticing.
	key := []byte("src/internal/pkg001/service_0042.go")
	got := testing.AllocsPerRun(1000, func() { sinkHash = core.HashBytes(core.DomainChunk, key) })
	require.Zero(t, got, "HashBytes must not allocate")
}

func TestHash_StringShortParse_RoundTrip(t *testing.T) {
	lower := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	for i := 0; i < 100; i++ {
		var h core.Hash
		_, err := rand.Read(h[:])
		require.NoError(t, err)

		s := h.String()
		require.True(t, lower.MatchString(s), "canonical form must be sha256: + 64 lowercase hex, got %q", s)
		require.True(t, strings.HasPrefix(s, "sha256:"))
		require.Len(t, h.Short(), 12)
		require.Equal(t, s[len("sha256:"):len("sha256:")+12], h.Short(), "Short is the first 12 hex chars, unprefixed")

		back, err := core.ParseHash(s)
		require.NoError(t, err)
		require.Equal(t, h, back)

		// The bare hex form parses too, so callers need not strip the prefix themselves.
		bare, err := core.ParseHash(hex.EncodeToString(h[:]))
		require.NoError(t, err)
		require.Equal(t, h, bare)
	}
}

func TestParseHash_Rejects(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"empty", ""},
		{"prefix only", "sha256:"},
		{"63 hex", strings.Repeat("a", 63)},
		{"65 hex", strings.Repeat("a", 65)},
		{"non-hex", "sha256:" + strings.Repeat("z", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := core.ParseHash(tc.in)
			require.Error(t, err)
			require.ErrorIs(t, err, core.ErrNotFound)
		})
	}
}

func TestHash_JSONRoundTrip(t *testing.T) {
	type holder struct {
		Root core.Hash `json:"root"`
	}
	h := core.HashBytes("qompack.chunk.v1", []byte("payload"))
	b, err := json.Marshal(holder{Root: h})
	require.NoError(t, err)
	require.Contains(t, string(b), `"sha256:`, "hashes serialize in the canonical text form")

	var got holder
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, h, got.Root)
}

func TestHash_UnmarshalJSON_Rejects(t *testing.T) {
	var h core.Hash
	require.Error(t, h.UnmarshalJSON([]byte(`"sha256:nope"`)))
	require.Error(t, h.UnmarshalJSON([]byte(`123`)))
}

// TestDomainRegistry_Distinct pins the domain-separation registry documented in hash.go. A later
// subplan that reuses one of these strings for a different meaning silently changes every key it
// derives, so the set is asserted distinct and stable here.
func TestDomainRegistry_Distinct(t *testing.T) {
	domains := []string{
		core.DomainChunk,
		core.DomainRoot,
		core.DomainNegKnow,
		core.DomainDecision,
		core.DomainArgs,
	}
	seen := map[string]bool{}
	for _, d := range domains {
		require.NotEmpty(t, d)
		require.True(t, strings.HasPrefix(d, "qompack."), "domain %q must be namespaced", d)
		require.False(t, seen[d], "duplicate domain %q", d)
		seen[d] = true
	}
	// Frozen spellings: changing one re-keys existing on-disk data.
	require.Equal(t, "qompack.chunk.v1", core.DomainChunk)
	require.Equal(t, "qompack.root.v1", core.DomainRoot)
	require.Equal(t, "qompack.neg.v1", core.DomainNegKnow)
	require.Equal(t, "qompack.decision", core.DomainDecision)
	require.Equal(t, "qompack.args.v1", core.DomainArgs)
}
