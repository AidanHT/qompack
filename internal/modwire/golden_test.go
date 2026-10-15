package modwire_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

// updateGolden is the package's -update flag: `go test ./internal/modwire -update` rewrites the
// goldens under testdata/ from the values below. It is the only way those files change.
var updateGolden = registerGoldenUpdateFlag()

// registerGoldenUpdateFlag registers -update, or adopts an existing registration of it.
func registerGoldenUpdateFlag() func() bool {
	if f := flag.Lookup("update"); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	v := flag.Bool("update", false, "rewrite golden files instead of comparing against them")
	return func() bool { return *v }
}

// requireGolden asserts got equals testdata/<name>, rewriting it under -update. Newlines are
// normalized on the read side only, so a Windows checkout with autocrlf on does not read as drift.
func requireGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if updateGolden() {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644)) // #nosec G306 -- test fixture
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden %s is missing; run go test ./internal/modwire -update", name)
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	require.Equal(t, string(want), string(got), "the wire bytes for %s drifted from the golden", name)
}

// The golden values. Each is a message as one side of the bridge writes it; the goldens are what
// the TypeScript side reads and writes, so a change here is a change to the published contract.
var (
	goldenHelloRequest = modwire.HelloRequest{
		V:           modwire.Version,
		SessionID:   "3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b",
		CWD:         "/home/dev/project",
		HostVersion: "2.1.300",
	}
	goldenBriefRequest = modwire.BriefRequest{
		V:                       modwire.Version,
		SessionID:               "3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b",
		CWD:                     "/home/dev/project",
		HostVersion:             "2.1.300",
		Trigger:                 "auto",
		UserInstructionsPresent: true,
		MaxChars:                modwire.BriefMaxChars,
	}
	goldenBriefRecord = modwire.BriefRecord{
		V:       modwire.Version,
		TS:      1_760_400_000_000,
		Trigger: "auto",
		Outcome: modwire.OutcomeServed,
		Chars:   812,
		MS:      143,
	}
	goldenHelloFile = modwire.HelloFile{
		V:           modwire.Version,
		SessionID:   "3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b",
		HostVersion: "2.1.300",
		TS:          1_760_400_000_000,
	}
)

// TestGolden_Messages freezes the exact bytes of every Stage A message and both records.
func TestGolden_Messages(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"hello_request.json", goldenHelloRequest},
		{"hello_response_enabled.json", modwire.HelloResponse{Status: modwire.Served(), Enabled: true}},
		{"hello_response_refused.json", modwire.HelloResponse{Status: modwire.Failure(modwire.ReasonRefused, "")}},
		{"brief_request.json", goldenBriefRequest},
		{"brief_response_served.json", modwire.BriefResponse{
			Status:       modwire.Served(),
			Instructions: modwire.BriefMarker + "\nKeep: the failing test is TestLedgerReplay; never edit db/.",
			FactIDs:      []string{"neg_7c1d2e3f4a5b", "pin_0a1b2c3d4e5f"},
		}},
		{"brief_response_cold.json", modwire.BriefResponse{Status: modwire.Failure(modwire.ReasonCold, "")}},
		{"error_unsupported_version.json", modwire.FailureFor(&modwire.VersionError{Got: 2})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := modwire.Encode(c.v)
			require.NoError(t, err)
			requireGolden(t, c.name, got)
		})
	}

	t.Run("brief_record.jsonl", func(t *testing.T) {
		got, err := goldenBriefRecord.Line()
		require.NoError(t, err)
		requireGolden(t, "brief_record.jsonl", got)
	})
	t.Run("hello_file.json", func(t *testing.T) {
		got, err := goldenHelloFile.Encode()
		require.NoError(t, err)
		requireGolden(t, "hello_file.json", got)
	})
}

// TestGolden_RequestsDecodeToTheirValues closes the loop for the requests the Go side reads: the
// committed bytes decode back to exactly the value that produced them.
func TestGolden_RequestsDecodeToTheirValues(t *testing.T) {
	if updateGolden() {
		t.Log("goldens are being rewritten; the round trip is checked on the next run")
		return
	}
	hello, err := os.ReadFile(filepath.Join("testdata", "hello_request.json"))
	require.NoError(t, err)
	gotHello, err := modwire.DecodeHelloRequest(bytes.NewReader(hello))
	require.NoError(t, err)
	require.Equal(t, goldenHelloRequest, gotHello)

	brief, err := os.ReadFile(filepath.Join("testdata", "brief_request.json"))
	require.NoError(t, err)
	gotBrief, err := modwire.DecodeBriefRequest(bytes.NewReader(brief))
	require.NoError(t, err)
	require.Equal(t, goldenBriefRequest, gotBrief)

	rec, err := os.ReadFile(filepath.Join("testdata", "brief_record.jsonl"))
	require.NoError(t, err)
	gotRec, err := modwire.ParseBriefRecord(bytes.TrimRight(rec, "\r\n"))
	require.NoError(t, err)
	require.Equal(t, goldenBriefRecord, gotRec)

	hf, err := os.ReadFile(filepath.Join("testdata", "hello_file.json"))
	require.NoError(t, err)
	gotHF, err := modwire.ParseHelloFile(hf)
	require.NoError(t, err)
	require.Equal(t, goldenHelloFile, gotHF)
}

// TestGolden_EveryMessageIsOneLine pins the framing: each message and record is one JSON object on
// one line, ending in a single newline, and carries "v":1 first.
func TestGolden_EveryMessageIsOneLine(t *testing.T) {
	if updateGolden() {
		return
	}
	for _, name := range []string{
		"hello_request.json", "hello_response_enabled.json", "hello_response_refused.json", "hello_response_below_min.json",
		"hello_response_steering.json",
		"brief_request.json", "brief_response_served.json", "brief_response_cold.json",
		"error_unsupported_version.json", "brief_record.jsonl", "hello_file.json",
	} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		require.NoError(t, err)
		s := strings.ReplaceAll(string(b), "\r\n", "\n")
		require.True(t, strings.HasSuffix(s, "\n"), "%s must end in a newline", name)
		require.Equal(t, 1, strings.Count(s, "\n"), "%s must be exactly one line", name)
		require.True(t, strings.HasPrefix(s, `{"v":1,`), "%s must open with the wire version", name)
	}
}
