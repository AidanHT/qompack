package hostperm

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// manyRules is a settings document with n distinct deny path rules, each the reviewer's shape: a
// globstar pattern whose miss is the costly case for the matcher.
func manyRules(n int) string {
	rules := make([]string, n)
	for i := range rules {
		rules[i] = "Read(src/**/gen" + strconv.Itoa(i) + "/*.x)"
	}
	return denyDoc(rules)
}

func denyDoc(rules []string) string {
	b, _ := json.Marshal(map[string]any{"permissions": map[string]any{"deny": rules}})
	return string(b)
}

// TestAnUnboundedRuleCountFailsClosed is C1.9 review finding 2: nothing bounded the number of Read
// rules, and every path a request checks costs time in proportion to it, so a committed settings
// file could make each retrieval arbitrarily slow. Past the cap the policy is unusable, which fails
// closed exactly as a malformed rule does.
func TestAnUnboundedRuleCountFailsClosed(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.project(), manyRules(20000))
	_, err := e.pol.Snapshot()
	require.Error(t, err, "20000 Read rules must not be evaluated on every retrieval")
	require.True(t, errors.Is(err, ErrUnavailable))
}

// TestTheRuleCapIsSummedAcrossSources pins the cap's boundary: exactly maxReadPatterns rules load,
// and one more fails closed even when no single file holds more than half of them.
func TestTheRuleCapIsSummedAcrossSources(t *testing.T) {
	e := newDiskEnv(t)
	half := maxReadPatterns / 2
	e.write(t, e.project(), manyRules(half))
	e.write(t, filepath.Join(e.home, ".claude", "settings.json"), manyRules(maxReadPatterns-half))
	rs, err := e.pol.Snapshot()
	require.NoError(t, err, "exactly the cap is in force")
	require.Equal(t, Deny, rs.Evaluate(filepath.Join(e.root, "src", "gen7", "a.x")).Effect)

	e.write(t, filepath.Join(e.home, ".claude", "settings.json"), manyRules(maxReadPatterns-half+1))
	_, err = e.pol.Snapshot()
	require.ErrorIs(t, err, ErrUnavailable)
	require.Contains(t, err.Error(), strconv.Itoa(maxReadPatterns)+" patterns")
}

// TestTheSegmentCapBoundsAFewLongRules: the rule count alone does not bound the work, because one
// rule may spell millions of segments inside the file-size bound.
func TestTheSegmentCapBoundsAFewLongRules(t *testing.T) {
	long := func(n int) string { return "Read(" + strings.Repeat("**/", n-1) + "x)" }
	e := newDiskEnv(t)
	e.write(t, e.project(), denyDoc([]string{long(maxReadSegments)}))
	_, err := e.pol.Snapshot()
	require.NoError(t, err, "exactly the segment cap is in force")

	e.write(t, e.project(), denyDoc([]string{long(maxReadSegments), "Read(./a/b)"}))
	_, err = e.pol.Snapshot()
	require.ErrorIs(t, err, ErrUnavailable)
	require.Contains(t, err.Error(), strconv.Itoa(maxReadSegments)+" path segments")
}
