package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// The fuzz target for the JSON-RPC transport.
//
// Everything a host sends arrives here first, and the server has no say in what that is: a
// truncated write from a killed client, a NUL from a mis-decoded pipe, a frame larger than any
// limit. §12.1 makes the requirement absolute — a malformed line is answered and the loop
// continues — so the property under test is not "the right answer" but "an answer, and still
// running".

// mcpCorpusDir holds the seed corpus. It is the repository-level testdata tree rather than
// internal/mcp/testdata/ because these are WIRE shapes, and every other frozen wire shape in this
// project lives there.
const mcpCorpusDir = "../../testdata/corpora/mcp"

// corpusNonSeeds are the files in that directory that are not fuzz seeds: the README that explains
// the generated 2 MiB case, and the hand-drive script for the smoke check. The script is
// newline-framed, so it is a conversation rather than one request line, and feeding it as a single
// seed would say something the corpus does not mean.
var corpusNonSeeds = map[string]bool{
	"README.md":         true,
	"initialize.ndjson": true,
}

// fuzzHugeLineBytes is the generated sixth seed: one line of 2 MiB, twice defaultMaxLine, so the
// accepted-line ceiling and the resynchronization after it are both on the seed path from the
// first iteration.
//
// It is generated rather than committed because what it exercises is a property of its LENGTH,
// which one line of code states and two million bytes of 'a' do not — and a blob that size is
// reviewed once, never again, and paid for by every clone forever.
const fuzzHugeLineBytes = 2 << 20

// FuzzServeLine feeds arbitrary bytes to Serve as one request line and asserts the server answers
// or ignores it, never fails and never panics.
//
// Termination is enforced by the fuzzing engine's own per-input deadline rather than by a context
// here: a wall-clock timeout inside the target would be a second clock in a package that drives
// every other one through a seam, and it would report a slow machine as a hang.
func FuzzServeLine(f *testing.F) {
	entries, err := os.ReadDir(mcpCorpusDir)
	require.NoError(f, err, "reading the fuzz seed corpus %s", mcpCorpusDir)

	for _, e := range entries {
		if e.IsDir() || corpusNonSeeds[e.Name()] {
			continue
		}
		seed, readErr := os.ReadFile(filepath.Join(mcpCorpusDir, e.Name()))
		require.NoError(f, readErr, "reading fuzz seed %s", e.Name())
		f.Add(seed)
	}
	f.Add(bytes.Repeat([]byte("a"), fuzzHugeLineBytes))

	f.Fuzz(func(t *testing.T, data []byte) {
		s := NewServerWithOptions(ServerOptions{
			Name: ServerName, Version: protoVersionUnderTest, Log: logging.Nop(),
		})
		// The eight real tools, bound to nothing. A zero ToolDeps is a supported wiring (it is
		// what RegisterProxy and mcptest use), so registering them costs no I/O and puts the
		// schema compiler and the argument validator on the fuzz path — which is where a
		// fuzz-generated `tools/call` argument object actually lands.
		require.NoError(t, RegisterAll(s, ToolDeps{Clock: newFakeClock(epoch), Log: logging.Nop()}))

		// The trailing newline is what makes the input a FRAME. Without it a generated input with
		// no newline would only ever exercise the unterminated-tail path.
		in := io.MultiReader(bytes.NewReader(data), bytes.NewReader([]byte("\n")))

		var out bytes.Buffer
		require.NoError(t, s.Serve(context.Background(), in, &out),
			"Serve must answer or ignore any line; only a dead writer ends it with an error")

		// Whatever it decided to say, the framing has to have survived saying it: one JSON object
		// per line. A response that carried raw input bytes into the stream unescaped would
		// desynchronize the host permanently, and this is the assertion that would catch it.
		for _, line := range bytes.Split(bytes.TrimRight(out.Bytes(), "\n"), []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var probe map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(line, &probe), "output line is not one JSON object: %q", line)
			require.Contains(t, probe, "jsonrpc", "every response line carries the protocol version: %q", line)
		}
	})
}
