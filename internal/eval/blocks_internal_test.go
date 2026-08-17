package eval

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestApproachClass_Normalization is the property the elimination demand keys off: an approach
// only matches an earlier elimination through this normal form, so the normal form must be closed
// (its own output is a fixed point) and must never leave the [a-z0-9-] alphabet.
func TestApproachClass_Normalization(t *testing.T) {
	shape := regexp.MustCompile(`^[a-z0-9-]*$`)
	rapid.Check(t, func(rt *rapid.T) {
		in := rapid.String().Draw(rt, "approach")
		got := approachClass(in)
		if !shape.MatchString(got) {
			rt.Fatalf("approachClass(%q) = %q, which leaves the [a-z0-9-] alphabet", in, got)
		}
		if again := approachClass(got); again != got {
			rt.Fatalf("approachClass is not idempotent: %q -> %q -> %q", in, got, again)
		}
	})
}

// TestApproachClass_KnownForm pins the example the implementation spec states.
func TestApproachClass_KnownForm(t *testing.T) {
	require.Equal(t, "widen-pool-timeout", approachClass("Widen  Pool Timeout!"))
}
