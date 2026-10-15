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
	// The record's field set is closed: ts, trigger, outcome, chars, ms. Adding a field that could
	// carry brief text would put content in a file the eval reads and the user never asked for.
	line, err := modwire.BriefRecord{TS: 1, Trigger: "manual", Outcome: modwire.OutcomeCold}.Line()
	require.NoError(t, err)
	require.Equal(t, `{"ts":1,"trigger":"manual","outcome":"cold","chars":0,"ms":0}`+"\n", string(line))
}

func TestBriefRecord_Validation(t *testing.T) {
	bad := []modwire.BriefRecord{
		{TS: 1, Trigger: "auto", Outcome: "napping"},
		{TS: 1, Trigger: "auto", Outcome: modwire.OutcomeServed, Chars: -1},
		{TS: 1, Trigger: "auto", Outcome: modwire.OutcomeServed, MS: -1},
		{TS: 1, Trigger: "", Outcome: modwire.OutcomeServed},
		{TS: 1, Trigger: "auto", Outcome: modwire.OutcomeCold, Chars: 5},
		{TS: 1, Trigger: "auto", Outcome: modwire.OutcomeServed, Chars: modwire.BriefMaxChars + 1},
	}
	for i, r := range bad {
		_, err := r.Line()
		require.ErrorIs(t, err, modwire.ErrInvalid, "case %d", i)
	}

	for _, in := range []string{
		`{"ts":1,"trigger":"auto","outcome":"napping","chars":0,"ms":0}`,
		`{"ts":1,"trigger":"auto","outcome":"served","chars":0,"ms":0} trailing`,
		`not json`,
	} {
		_, err := modwire.ParseBriefRecord([]byte(in))
		require.Error(t, err, in)
	}

	// A reader tolerates fields a later writer adds, as every wire message does.
	r, err := modwire.ParseBriefRecord([]byte(`{"ts":9,"trigger":"auto","outcome":"busy","chars":0,"ms":3,"x":1}`))
	require.NoError(t, err)
	require.Equal(t, modwire.OutcomeBusy, r.Outcome)
}

func TestHelloFile_Validation(t *testing.T) {
	for i, h := range []modwire.HelloFile{
		{SessionID: "", HostVersion: "2.1.300", TS: 1},
		{SessionID: "../s", HostVersion: "2.1.300", TS: 1},
		{SessionID: "s", HostVersion: "", TS: 1},
	} {
		_, err := h.Encode()
		require.ErrorIs(t, err, modwire.ErrInvalid, "case %d", i)
	}
	_, err := modwire.ParseHelloFile([]byte(`{"session_id":"s","host_version":"","ts":1}`))
	require.ErrorIs(t, err, modwire.ErrInvalid)
}
