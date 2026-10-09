package eval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHasQompackMarker_RecognizesBothSpellings pins that a live run counts a rehydration block
// under the 0.3.2 marker and under the one 0.3.x and earlier wrote.
func TestHasQompackMarker_RecognizesBothSpellings(t *testing.T) {
	require.True(t, hasQompackMarker("<!-- qompack:session-record seq=1 ver=1 -->\n# x"))
	require.True(t, hasQompackMarker("<!-- qompack:injected seq=1 ver=1 -->\n# x"))
	require.False(t, hasQompackMarker("<!-- qompack-contract-probe qompack-contract-1 -->"))
}
