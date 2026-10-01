package hostperm

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These rows pin how deep the walk of the server-managed cache goes (D56(d)). Its envelope is not
// documented, so a permissions block applies wherever it sits: no depth drops one silently. The only
// bound left is encoding/json's own nesting limit, and a document past it fails closed, as any
// undecodable source does.

// jsonNestingLimit is encoding/json's maxNestingDepth (scanner.go), which is not exported.
const jsonNestingLimit = 10000

// wrap nests inner inside depth containers, alternating an object and an array when mixed is set,
// so inner's own object sits depth levels below the top of the document.
func wrap(inner string, depth int, mixed bool) string {
	var open strings.Builder
	closing := make([]byte, depth)
	for i := 0; i < depth; i++ {
		closing[depth-1-i] = '}'
		if mixed && i%2 == 1 {
			open.WriteString(`[{"pad":1},`)
			closing[depth-1-i] = ']'
			continue
		}
		open.WriteString(`{"pad":1,"layer":`)
	}
	return open.String() + inner + string(closing)
}

func (e *diskEnv) remote() string { return filepath.Join(e.home, ".claude", "remote-settings.json") }

func TestServerManagedRulesApplyAtAnyDepth(t *testing.T) {
	const justPastTheOldBound = 9 // the walk used to stop after 8 levels
	const farDeeper = 100
	for _, tc := range []struct {
		name  string
		depth int
		mixed bool
	}{
		{"nine objects deep", justPastTheOldBound, false},
		{"a hundred objects deep", farDeeper, false},
		{"a hundred objects and arrays deep", farDeeper, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDiskEnv(t)
			e.write(t, e.remote(), wrap(`{"permissions":{"deny":["Read(./a.txt)"],"ask":["Read(./b.txt)"]}}`,
				tc.depth, tc.mixed))
			require.Equal(t, Deny, e.check(t, "a.txt"))
			require.Equal(t, Ask, e.check(t, "b.txt"))
			require.Equal(t, Allow, e.check(t, "c.txt"))
		})
	}
}

func TestADeepServerManagedDocumentWithoutPermissionsStaysEnforced(t *testing.T) {
	const deep = 500
	e := newDiskEnv(t)
	e.write(t, e.remote(), wrap(`{"note":"no rules here"}`, deep, true))
	e.write(t, e.user(), `{"permissions":{"deny":["Read(./a.txt)"]}}`)
	require.Equal(t, Deny, e.check(t, "a.txt"), "the other sources still apply")
	require.Equal(t, Allow, e.check(t, "b.txt"), "a deep document with no rules refuses nothing")
}

func TestAServerManagedHooksBlockAtDepthSevenParses(t *testing.T) {
	// top(0) settings(1) hooks(2) PreToolUse[](3) entry(4) hooks[](5) hook(6) env(7).
	e := newDiskEnv(t)
	e.write(t, e.remote(), `{"settings":{"permissions":{"deny":["Read(./a.txt)"]},"hooks":{"PreToolUse":[`+
		`{"matcher":"Read","hooks":[{"type":"command","command":"x","env":{"K":"v"}}]}]}}}`)
	require.Equal(t, Deny, e.check(t, "a.txt"))
	require.Equal(t, Allow, e.check(t, "b.txt"))
}

func TestAServerManagedDocumentPastTheJSONNestingLimitFailsClosed(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.remote(), wrap(`{"permissions":{"deny":["Read(./a.txt)"]}}`, jsonNestingLimit, false))
	_, err := e.pol.Check(filepath.Join(e.root, "b.txt"))
	require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
	require.ErrorContains(t, err, "remote-settings.json")
}

func TestAServerManagedNumberFloat64CannotHoldIsNotRefused(t *testing.T) {
	// The walk decodes the whole document, so its numbers must decode as written: a value out of
	// float64's range is still valid JSON, and the host loads it.
	e := newDiskEnv(t)
	e.write(t, e.remote(), `{"ttl":1e400,"settings":{"permissions":{"deny":["Read(./a.txt)"],"n":1e400}}}`)
	require.Equal(t, Deny, e.check(t, "a.txt"))
	require.Equal(t, Allow, e.check(t, "b.txt"))
}
