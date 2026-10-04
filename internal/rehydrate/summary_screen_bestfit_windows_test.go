//go:build windows

package rehydrate

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf16"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// ansiCodePages are Windows' ANSI code pages, one of which the system locale makes the code page a
// program's ANSI command line (GetCommandLineA, a C program's argv) is converted into.
var ansiCodePages = []uint32{874, 932, 936, 949, 950, 1250, 1251, 1252, 1253, 1254, 1255, 1256, 1257, 1258}

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procWideCharToMultiByte = kernel32.NewProc("WideCharToMultiByte")
	procIsValidCodePage     = kernel32.NewProc("IsValidCodePage")
)

// bestFitUnmapped is the default character the measurement asks WideCharToMultiByte for, so a code
// point the code page cannot hold at all is told apart from one whose best fit is `?`.
const bestFitUnmapped = 0x01

// bestFitSeparator separates the code points of one conversion: U+0002, which every code page holds
// as itself and no double-byte code page uses as a trail byte.
const bestFitSeparator = 0x02

// toCodePage converts in to code page cp as a program's ANSI command line is converted: with
// best-fit mapping (no WC_NO_BEST_FIT_CHARS), and bestFitUnmapped for what has no mapping.
func toCodePage(t *testing.T, cp uint32, in []uint16) []byte {
	t.Helper()
	def := []byte{bestFitUnmapped, 0}
	n, _, err := procWideCharToMultiByte.Call(uintptr(cp), 0, uintptr(unsafe.Pointer(&in[0])), uintptr(len(in)),
		0, 0, uintptr(unsafe.Pointer(&def[0])), 0)
	require.NotZero(t, n, "WideCharToMultiByte(%d): %v", cp, err)
	out := make([]byte, n)
	m, _, err := procWideCharToMultiByte.Call(uintptr(cp), 0, uintptr(unsafe.Pointer(&in[0])), uintptr(len(in)),
		uintptr(unsafe.Pointer(&out[0])), n, uintptr(unsafe.Pointer(&def[0])), 0)
	require.Equal(t, n, m, "WideCharToMultiByte(%d): %v", cp, err)
	return out
}

// ansiBestFitPunctuation measures, for every code point the whitelist would read as a letter, a mark
// or a digit, its best fit in each ANSI code page, and returns those whose best fit is one ASCII
// character other than a letter or a digit, with the code pages and the characters.
func ansiBestFitPunctuation(t *testing.T) map[rune][]string {
	t.Helper()
	var cands []rune
	for r := rune(0x80); r <= unicode.MaxRune; r++ {
		if (r < 0xD800 || r > 0xDFFF) && (unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r)) {
			cands = append(cands, r)
		}
	}
	found := map[rune][]string{}
	for _, cp := range ansiCodePages {
		ok, _, _ := procIsValidCodePage.Call(uintptr(cp))
		require.NotZero(t, ok, "code page %d is installed", cp)
		const chunk = 512
		for lo := 0; lo < len(cands); lo += chunk {
			part := cands[lo:min(lo+chunk, len(cands))]
			var in []uint16
			for _, r := range part {
				in = append(append(in, utf16.Encode([]rune{r})...), bestFitSeparator)
			}
			outs := strings.Split(string(toCodePage(t, cp, in)), string(rune(bestFitSeparator)))
			if len(outs) != len(part)+1 {
				// A code point's own best fit holds the separator: convert the chunk one by one.
				outs = outs[:0]
				for _, r := range part {
					outs = append(outs, string(toCodePage(t, cp, utf16.Encode([]rune{r}))))
				}
			}
			for i, r := range part {
				o := outs[i]
				if len(o) != 1 || o[0] >= 0x80 || o[0] == bestFitUnmapped {
					continue
				}
				if c := o[0]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					found[r] = append(found[r], fmt.Sprintf("%d:%q", cp, c))
				}
			}
		}
	}
	return found
}

// TestWhitelist_NoANSIBestFitToPunctuationIsSafe measures Windows' ANSI best-fit conversion (D64's
// ruling on wave 19f's open items): no code point whose best fit in an ANSI code page is ASCII
// punctuation is whitelist-safe in a word or admitted in the root unit, and the measured set is the
// one bestFitANSI records for the rows that run on every platform.
func TestWhitelist_NoANSIBestFitToPunctuationIsSafe(t *testing.T) {
	found := ansiBestFitPunctuation(t)
	var got []rune
	for r := range found {
		got = append(got, r)
	}
	sort.Slice(got, func(a, b int) bool { return got[a] < got[b] })
	require.ElementsMatch(t, bestFitANSI, got, "the measured best fits to ASCII punctuation: %v", found)
	for _, r := range got {
		s := string(r)
		require.False(t, plainTokenSafe("a"+s+"b"), "U+%04X best-fits to %v", r, found[r])
		require.False(t, tokenSafe(`"a`+s+`b c"`), "U+%04X in a quoted run", r)
		require.False(t, urlTokenSafe("https://x.example/a"+s+"b"), "U+%04X in a URL", r)
		require.False(t, rootUnitAdmitted(previewRoot("a"+s+"b", "proj")), "U+%04X in the root", r)
	}
}
