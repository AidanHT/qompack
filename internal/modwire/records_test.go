package modwire_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

func TestRecordNames(t *testing.T) {
	name, err := modwire.BriefRecordName("3f2a-9c1e")
	require.NoError(t, err)
	require.Equal(t, "mod-brief-3f2a-9c1e.jsonl", name)

	name, err = modwire.HelloFileName("3f2a-9c1e")
	require.NoError(t, err)
	require.Equal(t, "mod-hello-3f2a-9c1e.json", name)

	require.Equal(t, "state", modwire.StateDir, "both records live under the store's state/ directory")

	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "a:b", "a b", strings.Repeat("s", modwire.MaxSessionIDBytes+1)} {
		_, err := modwire.BriefRecordName(bad)
		require.ErrorIs(t, err, modwire.ErrInvalid, "%q", bad)
		_, err = modwire.HelloFileName(bad)
		require.ErrorIs(t, err, modwire.ErrInvalid, "%q", bad)
		require.False(t, modwire.ValidSessionID(bad), "%q", bad)
	}
	require.True(t, modwire.ValidSessionID("3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b"))
	require.True(t, modwire.ValidSessionID("agent_1.fork"))
}

func TestBriefRecord_CarriesNoContent(t *testing.T) {
	// The record's field set is closed: v, ts, trigger, outcome, chars, ms. Adding a field that
	// could carry brief text would put content in a file the eval reads and the user never asked for.
	line, err := modwire.BriefRecord{V: modwire.Version, TS: 1, Trigger: "manual", Outcome: modwire.OutcomeCold}.Line()
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"ts":1,"trigger":"manual","outcome":"cold","chars":0,"ms":0}`+"\n", string(line))
}

func TestBriefRecord_Validation(t *testing.T) {
	const v = modwire.Version
	bad := []modwire.BriefRecord{
		{V: v, TS: 1, Trigger: "auto", Outcome: "napping"},
		{V: v, TS: 1, Trigger: "auto", Outcome: modwire.OutcomeServed, Chars: -1},
		{V: v, TS: 1, Trigger: "auto", Outcome: modwire.OutcomeServed, MS: -1},
		{V: v, TS: 1, Trigger: "", Outcome: modwire.OutcomeServed},
		{V: v, TS: 1, Trigger: "auto", Outcome: modwire.OutcomeCold, Chars: 5},
		{V: v, TS: 1, Trigger: "auto", Outcome: modwire.OutcomeServed, Chars: modwire.BriefMaxChars + 1},
	}
	for i, r := range bad {
		_, err := r.Line()
		require.ErrorIs(t, err, modwire.ErrInvalid, "case %d", i)
	}

	// A record carries the wire version as every message does: a writer that leaves it out, or a
	// line whose v is absent or another version, is refused rather than guessed at.
	for _, ver := range []int{0, 2} {
		_, err := modwire.BriefRecord{V: ver, TS: 1, Trigger: "auto", Outcome: modwire.OutcomeCold}.Line()
		require.ErrorIs(t, err, modwire.ErrUnsupportedVersion, "v %d", ver)
	}
	for _, in := range []string{
		`{"ts":1,"trigger":"auto","outcome":"cold","chars":0,"ms":0}`,
		`{"v":2,"ts":1,"trigger":"auto","outcome":"cold","chars":0,"ms":0}`,
	} {
		_, err := modwire.ParseBriefRecord([]byte(in))
		require.ErrorIs(t, err, modwire.ErrUnsupportedVersion, in)
	}

	for _, in := range []string{
		`{"v":1,"ts":1,"trigger":"auto","outcome":"napping","chars":0,"ms":0}`,
		`{"v":1,"ts":1,"trigger":"auto","outcome":"served","chars":0,"ms":0} trailing`,
		`not json`,
	} {
		_, err := modwire.ParseBriefRecord([]byte(in))
		require.Error(t, err, in)
	}

	// A reader tolerates fields a later writer adds, as every wire message does.
	r, err := modwire.ParseBriefRecord([]byte(`{"v":1,"ts":9,"trigger":"auto","outcome":"busy","chars":0,"ms":3,"x":1}`))
	require.NoError(t, err)
	require.Equal(t, modwire.OutcomeBusy, r.Outcome)
}

func TestHelloFile_Validation(t *testing.T) {
	const v = modwire.Version
	for i, h := range []modwire.HelloFile{
		{V: v, SessionID: "", HostVersion: "2.1.300", TS: 1},
		{V: v, SessionID: "../s", HostVersion: "2.1.300", TS: 1},
		{V: v, SessionID: "s", HostVersion: "", TS: 1},
		{V: v, SessionID: "s", HostVersion: "2.1.300", Steering: true, TS: 1},
	} {
		_, err := h.Encode()
		require.ErrorIs(t, err, modwire.ErrInvalid, "case %d", i)
	}
	_, err := modwire.ParseHelloFile(
		[]byte(`{"v":1,"session_id":"s","host_version":"","enabled":true,"steering":false,"ts":1}`))
	require.ErrorIs(t, err, modwire.ErrInvalid)

	for _, ver := range []int{0, 2} {
		_, err := modwire.HelloFile{V: ver, SessionID: "s", HostVersion: "2.1.300", Enabled: true, TS: 1}.Encode()
		require.ErrorIs(t, err, modwire.ErrUnsupportedVersion, "v %d", ver)
	}
	for _, in := range []string{
		`{"session_id":"s","host_version":"2.1.300","enabled":true,"steering":true,"ts":1}`,
		`{"v":2,"session_id":"s","host_version":"2.1.300","enabled":true,"steering":true,"ts":1}`,
	} {
		_, err := modwire.ParseHelloFile([]byte(in))
		require.ErrorIs(t, err, modwire.ErrUnsupportedVersion, in)
	}
}

// TestHelloFile_RecordsWhatTheHelloAnswered: the hello file carries the served hello's enabled and
// steering answers. A reader can never take an absent answer for false: a file that lacks either
// key, or carries it as null, is refused, as an absent "v" is. Steering true implies enabled true,
// as on the hello response.
func TestHelloFile_RecordsWhatTheHelloAnswered(t *testing.T) {
	for _, tc := range []struct{ enabled, steering bool }{{false, false}, {true, false}, {true, true}} {
		h := modwire.HelloFile{
			V: modwire.Version, SessionID: "s", HostVersion: "2.1.300",
			Enabled: tc.enabled, Steering: tc.steering, TS: 1,
		}
		b, err := h.Encode()
		require.NoError(t, err)
		got, err := modwire.ParseHelloFile(b)
		require.NoError(t, err)
		require.Equal(t, h, got)
	}

	for _, in := range []string{
		`{"v":1,"session_id":"s","host_version":"2.1.300","steering":false,"ts":1}`,
		`{"v":1,"session_id":"s","host_version":"2.1.300","enabled":false,"ts":1}`,
		`{"v":1,"session_id":"s","host_version":"2.1.300","ts":1}`,
		`{"v":1,"session_id":"s","host_version":"2.1.300","enabled":null,"steering":false,"ts":1}`,
		`{"v":1,"session_id":"s","host_version":"2.1.300","enabled":true,"steering":null,"ts":1}`,
		`{"v":1,"session_id":"s","host_version":"2.1.300","enabled":false,"steering":true,"ts":1}`,
	} {
		_, err := modwire.ParseHelloFile([]byte(in))
		require.ErrorIs(t, err, modwire.ErrInvalid, in)
	}
}
