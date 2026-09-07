package mcp

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// The seeded corpus every tool test runs against: six files, forty tool uses, and one 200 000-byte
// TypeScript source whose symbol offsets are known exactly.
//
// The sizes here are EXACT, not approximate, and that is the whole reason this file exists. A span
// test asserts things like `NextSpan == "262144:137856"`, which is only meaningful if the object is
// exactly 400 000 canonical bytes; a symbol-widening test asserts that a chunk-aligned cut lands
// mid-function, which is only reproducible if the function is at a known offset. Generating the
// content to a byte budget is what makes those assertions statements about the resolver rather than
// statements about whatever the generator happened to emit.
//
// Content is generated rather than checked in because 600 KB of fixture text in the repository
// would be reviewed once and never again, and because a generator can state its invariants — see
// the require calls in buildAuthTS.

// The seeded object sizes. §11.6 forbids bare literals of this shape in production code; these are
// test fixtures, where the number IS the specification.
const (
	// authTotalBytes is the canonical size of src/auth.ts.
	authTotalBytes = 200000
	// refreshTokenOff and refreshTokenEnd bracket refreshToken's body inside src/auth.ts.
	refreshTokenOff = 18200
	refreshTokenEnd = 19900
	// bigOutputBytes is the canonical size of the seeded 200 KB bash output.
	bigOutputBytes = 200 * 1024
	// hugeObjectBytes is the canonical size of the object that exceeds runtime.mcp.maxResponseBytes,
	// chosen so that the first page is 262144 bytes and the remainder is exactly 137856.
	hugeObjectBytes = 400000
	// seededToolUses is how many tool uses the corpus records.
	seededToolUses = 40
)

// corpus is the seeded fixture's index: what was written, where, and at which offsets.
type corpus struct {
	// AuthRoot is src/auth.ts's root hash, and AuthText its exact content.
	AuthRoot core.Hash
	AuthText string
	// AuthUse is the tool_use_id of the FileRead that produced src/auth.ts.
	AuthUse core.ToolUseID
	// HugeRoot is the 400 000-byte object the response-cap tests page through.
	HugeRoot core.Hash
	HugeText string
	// HugeUse is that object's tool_use_id.
	HugeUse core.ToolUseID
	// Files maps each seeded path to its content.
	Files map[string]string
	// Uses is every tool_use_id the corpus recorded, in recording order.
	Uses []core.ToolUseID
}

// seed writes the corpus into f and returns its index.
//
// It is a method on fixture rather than an option so that a test which needs the corpus pays for
// it and a test which does not — the schema tests, the promoter tests — starts in a tenth of the
// time. The plan's `newFixture(t)` is the empty one; `newSeededFixture(t)` is this.
func (f *fixture) seed(t *testing.T) *corpus {
	t.Helper()
	require.NotNil(t, f.Store, "seed needs a store")

	c := &corpus{Files: map[string]string{}}

	c.AuthText = buildAuthTS(t)
	if f.huge {
		c.HugeText = buildFiller("huge", hugeObjectBytes)
	}

	files := []struct{ path, body, tool string }{
		{"src/auth.ts", c.AuthText, "Read"},
		{"src/pool.ts", buildPoolTS(), "Read"},
		{"src/index.ts", "import { login } from \"./auth\";\n\nlogin();\n", "Read"},
		{"docker-compose.yml", "services:\n  db:\n    image: postgres:16\n", "Read"},
		{"package-lock.json", "{\n  \"name\": \"fixture\",\n  \"lockfileVersion\": 3\n}\n", "Read"},
		{"README.md", "# Fixture\n\nA seeded project for the MCP retrieval tests.\n", "Read"},
	}

	turn := core.TurnIndex(1)
	for _, file := range files {
		c.Files[file.path] = file.body
		root, id := f.putAndRecord(t, file.tool, file.path, file.body, turn)
		c.Uses = append(c.Uses, id)
		if file.path == "src/auth.ts" {
			c.AuthRoot, c.AuthUse = root, id
		}
		turn++
	}

	// The 200 KB bash output and the 400 000-byte object are tool uses with no worktree file: they
	// are what `expand` is FOR — content that was in context, is not any more, and only the store
	// still has.
	c.Uses = append(c.Uses, f.record(t, "Bash", "", buildFiller("bash", bigOutputBytes), turn))
	turn++
	if f.huge {
		c.HugeRoot, c.HugeUse = f.putAndRecord(t, "Bash", "", c.HugeText, turn)
		c.Uses = append(c.Uses, c.HugeUse)
		turn++
	}

	// Pad to forty. Every filler mentions "pool timeout" a varying number of times so that the
	// recall ranking has something to order, and names its own index so a failure can point at one.
	for len(c.Uses) < seededToolUses {
		i := len(c.Uses)
		body := fmt.Sprintf("run %d\npool timeout after %d ms\n%s\n", i, i*10, strings.Repeat("log line\n", i))
		c.Uses = append(c.Uses, f.record(t, "Bash", "", body, turn))
		turn++
	}

	return c
}

// newSeededFixture is newFixture plus the corpus.
func newSeededFixture(t *testing.T, opts ...fixtureOpt) (*fixture, *corpus) {
	t.Helper()
	f := newFixture(t, opts...)
	return f, f.seed(t)
}

// rootOfUse looks a recorded tool use's root hash back up.
func (f *fixture) rootOfUse(t *testing.T, id core.ToolUseID) core.Hash {
	t.Helper()
	rec, err := f.Store.ToolUse(t.Context(), id)
	require.NoError(t, err, "ToolUse(%s)", id)
	return rec.Root
}

// buildAuthTS generates src/auth.ts: exactly authTotalBytes bytes, with refreshToken's body
// spanning [refreshTokenOff, refreshTokenEnd) and two other exported functions around it.
//
// The layout is asserted rather than assumed. A generator that silently drifted by one byte would
// turn every span assertion downstream into a mystery, so the offsets are checked here — where the
// failure names the generator — instead of forty assertions later.
func buildAuthTS(t *testing.T) string {
	t.Helper()

	var b strings.Builder

	header := "// src/auth.ts — seeded fixture for the MCP span tests.\n" +
		"import { Pool } from \"pg\";\n\n" +
		"export function login(user: string): boolean {\n" +
		"  return user.length > 0;\n" +
		"}\n\n"
	b.WriteString(header)

	// Pad up to refreshTokenOff with comment lines, so the padding is valid TypeScript and a human
	// reading a failure sees a file rather than noise.
	padTo(&b, refreshTokenOff, "// filler ")

	refresh := "export function refreshToken(token: string): string {\n" +
		"  // The symbol the widening tests anchor on.\n"
	b.WriteString(refresh)
	// Pad the body out so the function ENDS exactly at refreshTokenEnd, closing brace included.
	padTo(&b, refreshTokenEnd-len("}\n"), "  // body ")
	b.WriteString("}\n")
	require.Equal(t, refreshTokenEnd, b.Len(), "refreshToken must end exactly at refreshTokenEnd")

	tail := "\nexport function logout(): void {\n  return;\n}\n\n"
	b.WriteString(tail)
	padTo(&b, authTotalBytes, "// tail ")

	out := b.String()
	require.Len(t, out, authTotalBytes, "src/auth.ts must be exactly authTotalBytes")
	require.Equal(t, "export function refreshToken", out[refreshTokenOff:refreshTokenOff+len("export function refreshToken")],
		"refreshToken must start exactly at refreshTokenOff")
	return out
}

// padTo appends prefix-numbered comment lines until b is exactly n bytes long.
//
// The last line is trimmed to land on n exactly, which is why the prefix is a comment: a truncated
// comment is still a comment, whereas a truncated statement would not parse.
func padTo(b *strings.Builder, n int, prefix string) {
	for i := 0; b.Len() < n; i++ {
		line := prefix + strconv.Itoa(i) + "\n"
		if remaining := n - b.Len(); remaining < len(line) {
			// Fill the remainder with the prefix's own bytes and close the line, so the result is
			// still newline-terminated at exactly n.
			line = strings.Repeat("-", remaining-1) + "\n"
			if remaining == 1 {
				line = "\n"
			}
		}
		b.WriteString(line)
	}
}

// buildPoolTS is the file the text-search tests match against.
func buildPoolTS() string {
	return "// src/pool.ts\n" +
		"export const poolTimeoutMs = 30_000;\n\n" +
		"export function connect(): void {\n" +
		"  // A pool timeout here means pgbouncer is in transaction mode.\n" +
		"}\n"
}

// buildFiller generates exactly n bytes of labelled, newline-terminated filler.
func buildFiller(label string, n int) string {
	var b strings.Builder
	b.Grow(n)
	padTo(&b, n, "// "+label+" ")
	return b.String()
}
