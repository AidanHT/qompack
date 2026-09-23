//go:build windows

package paths

import (
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// longByFullPath is Long as it was before its short-absolute shortcut: every non-prefixed path goes
// through filepath.Abs (GetFullPathNameW) and is prefixed only when the full path reaches the
// threshold. The shortcut must return exactly what this returns.
func longByFullPath(p string) string {
	if p == "" || strings.HasPrefix(p, longPrefix) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	abs = filepath.Clean(abs)
	if len(abs) < longPathThreshold {
		return p
	}
	if strings.HasPrefix(abs, `\\`) {
		return longUNCPrefix + strings.TrimPrefix(abs, `\\`)
	}
	return longPrefix + abs
}

// TestLong_ShortAbsoluteFastPathMatchesTheFullPathRule compares Long with the full-path rule over
// the path shapes full-path normalization rewrites — forward slashes, doubled separators, "." and
// ".." elements, trailing dots and spaces, reserved device names, UNC and device-namespace roots —
// at every length from well under the threshold to well over it, plus relative and drive-relative
// paths, which the shortcut must leave to the general rule.
func TestLong_ShortAbsoluteFastPathMatchesTheFullPathRule(t *testing.T) {
	heads := []string{`C:\`, `c:/`, `D:\work\`, `\\server\share\`, `//server/share/`, `\\.\C:\`, `C:`, `\`, ``, `.\`, `..\`}
	elems := []string{
		"a", "project", ".qompack", "objects", "ab", ".", "..", "x.", "y ", "z. .", "CON", "nul",
		"COM1.txt", "a/b", `c\\d`, "...", "tried.bloom", strings.Repeat("q", 40), strings.Repeat("w", 97),
	}
	rng := rand.New(rand.NewPCG(3, 4))
	checked := 0
	for i := 0; i < 40000; i++ {
		var b strings.Builder
		b.WriteString(heads[rng.IntN(len(heads))])
		for b.Len() < 180+rng.IntN(100) {
			b.WriteString(elems[rng.IntN(len(elems))])
			if rng.IntN(5) == 0 {
				b.WriteByte('/')
			} else {
				b.WriteByte('\\')
			}
		}
		b.WriteString(elems[rng.IntN(len(elems))])
		p := b.String()
		for _, cut := range []int{len(p), longPathThreshold - 1, longPathThreshold, longPathThreshold + 1} {
			if cut > len(p) || cut <= 0 {
				continue
			}
			q := p[:cut]
			require.Equal(t, longByFullPath(q), Long(q), "Long(%q)", q)
			checked++
		}
	}
	require.Greater(t, checked, 40000, "fixture sanity: the generator must produce paths on both sides of the threshold")
}
