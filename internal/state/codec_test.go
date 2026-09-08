package state_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/state"
	"github.com/stretchr/testify/require"
)

// goldenV1 is a frozen SnapshotVersion 1 document, written by hand rather than by Encode. It is
// the durability contract: a document another version of this package wrote must keep decoding
// into the same meaning after any later edit here. A change that breaks it is a wire-format
// change and needs a new SnapshotVersion with its own reader, not an edit to this constant.
const goldenV1 = `{
  "v": 1,
  "records": [
    {
      "v": 1,
      "id": "r1",
      "authority": "explicit_decision",
      "scope": {"worktree": "C:/proj", "session": "sess-a"},
      "claim": "use tabs",
      "dependencies": {
        "deps": [{"path": "src/a.go", "hash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
        "coverage": "complete"
      },
      "validity": {"from": 1, "to": 4, "generation": 2},
      "sources": ["obs-a", "obs-b"],
      "transform_version": "state/v1",
      "hash_version": "sha256/v1",
      "lineage": {
        "superseded_by": ["r2"],
        "corrected_by": ["r2"],
        "history": [
          {"kind": "superseded", "other": "r2", "authority": "user_correction", "turn": 7, "note": "reversed"}
        ]
      },
      "recorded_at": 1700000000000
    },
    {
      "v": 1,
      "id": "r2",
      "authority": "user_correction",
      "scope": {"worktree": "C:/proj", "session": "sess-a"},
      "claim": "use spaces",
      "dependencies": {"coverage": "unknown", "reason": "index drain gap"},
      "validity": {"from": 5, "to": 0, "generation": 2},
      "sources": ["obs-c"],
      "transform_version": "state/v1",
      "hash_version": "sha256/v1",
      "lineage": {"supersedes": ["r1"], "corrects": ["r1"]}
    }
  ]
}`

func TestDecode_GoldenV1DocumentStaysReadable(t *testing.T) {
	s, err := state.Decode([]byte(goldenV1))
	require.NoError(t, err)
	require.Equal(t, 2, s.Len())

	old, ok := s.Get("r1")
	require.True(t, ok)
	require.Equal(t, core.AuthorityExplicitDecision, old.Authority)
	require.Equal(t, "use tabs", old.Claim)
	require.Equal(t, state.SessionScope("C:/proj", "sess-a"), old.Scope)
	require.Equal(t, state.DepCoverageComplete, old.Dependencies.Coverage)
	require.Len(t, old.Dependencies.Deps, 1)
	require.Equal(t, "src/a.go", old.Dependencies.Deps[0].Path)
	require.Equal(t, core.EvidenceValidity{From: 1, To: 4, Generation: 2}, old.Validity)
	require.Equal(t, []core.ObservationID{"obs-a", "obs-b"}, old.Sources)
	require.Equal(t, state.TransformVersion, old.TransformVersion)
	require.Equal(t, core.EvidenceHashVersion, old.HashVersion)
	require.True(t, old.Superseded())
	require.Equal(t, []state.RecordID{"r2"}, old.Lineage.CorrectedBy)
	require.Len(t, old.Lineage.History, 1)
	require.Equal(t, state.LineageSuperseded, old.Lineage.History[0].Kind)
	require.Equal(t, core.UnixMilli(1700000000000), old.RecordedAt)

	fix, ok := s.Get("r2")
	require.True(t, ok)
	require.Equal(t, state.DepCoverageUnknown, fix.Dependencies.Coverage)
	require.Equal(t, "index drain gap", fix.Dependencies.Reason)

	// The supersession the document records still governs what applies.
	require.Equal(t, []string{"r2"}, ids(s.Applicable(state.SessionScope("C:/proj", "sess-a"))))
}

func TestEncodeDecode_RoundTripsLineageAndConflict(t *testing.T) {
	s := state.NewSet()
	require.NoError(t, s.Add(rec("a1", core.AuthorityToolObservation, scopeA, "port is 8080")))
	require.NoError(t, s.Add(rec("b1", core.AuthorityCandidateExtraction, scopeA, "port is 9090")))
	require.NoError(t, s.Conflict(rec("x1", core.AuthorityConflict, scopeA, "port disagreement"),
		[]state.RecordID{"a1", "b1"}, "two ports observed", 5))
	require.NoError(t, s.Add(rec("w1", core.AuthorityUserCorrection, scopeWT, "worktree rule")))

	b, err := s.Encode()
	require.NoError(t, err)

	var envelope struct {
		Version int `json:"v"`
	}
	require.NoError(t, json.Unmarshal(b, &envelope))
	require.Equal(t, state.SnapshotVersion, envelope.Version, "every document is version-stamped")

	back, err := state.Decode(b)
	require.NoError(t, err)
	require.Equal(t, s.All(), back.All())
	require.Equal(t, ids(s.Conflicts(scopeA)), ids(back.Conflicts(scopeA)))
	require.Equal(t, ids(s.Applicable(scopeA)), ids(back.Applicable(scopeA)))

	// A decoded conflict is still a conflict: decoding is not a resolution.
	require.Len(t, back.Conflicts(scopeA), 1)
	require.False(t, back.Conflicts(scopeA)[0].Conflict.Resolved())
}

func TestDecode_RefusesDocumentsItCannotReadRatherThanGuessing(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"unversioned legacy document", `{"records":[]}`},
		{"future snapshot version", `{"v":2,"records":[]}`},
		{"negative version", `{"v":-1,"records":[]}`},
		{"not json", `{`},
		{"unversioned record inside a v1 snapshot", strings.Replace(goldenV1, `"v": 1,
      "id": "r1"`, `"id": "r1"`, 1)},
		{"duplicate record id", `{"v":1,"records":[` + oneRecord("dup") + `,` + oneRecord("dup") + `]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := state.Decode([]byte(tc.doc))
			require.Error(t, err)
			require.ErrorIs(t, err, core.ErrContract)
		})
	}
}

// oneRecord is the smallest record a v1 document can carry.
func oneRecord(id string) string {
	return `{"v":1,"id":"` + id + `","authority":"hypothesis",` +
		`"scope":{"worktree":"C:/proj","session":"sess-a"},"claim":"c",` +
		`"dependencies":{"coverage":"complete"},"validity":{"from":1,"to":0,"generation":1},` +
		`"sources":["obs-1"],"transform_version":"state/v1","hash_version":"sha256/v1"}`
}

// A later writer's labels must survive a read without acquiring a meaning this version does not
// have. Core's rule is that an unknown authority is retained as unknown, never treated as user
// authority — so the record is kept and readable, and is simply never applicable.
func TestDecode_UnknownLabelsAreRetainedButNeverApplicable(t *testing.T) {
	doc := `{"v":1,"records":[` +
		strings.Replace(oneRecord("f1"), `"authority":"hypothesis"`, `"authority":"user_ratified"`, 1) +
		`,` + strings.Replace(oneRecord("f2"), `"coverage":"complete"`, `"coverage":"mostly"`, 1) +
		`,` + strings.Replace(oneRecord("f3"), `"validity":{"from":1,"to":0,"generation":1}`,
		`"validity":{"from":9,"to":2,"generation":1}`, 1) +
		`]}`

	s, err := state.Decode([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, 3, s.Len(), "an unreadable field drops no record")

	future, ok := s.Get("f1")
	require.True(t, ok)
	require.Equal(t, core.Authority("user_ratified"), future.Authority,
		"the label is retained verbatim, not rewritten")
	require.False(t, future.Authority.Valid())
	require.NotContains(t, ids(s.Applicable(scopeA)), "f1",
		"a label this version cannot classify never carries authority")

	coverage, ok := s.Get("f2")
	require.True(t, ok)
	require.Equal(t, state.DepCoverageUnknown, coverage.Dependencies.Coverage,
		"an unreadable coverage label reads as unknown, never as complete")

	validity, ok := s.Get("f3")
	require.True(t, ok)
	require.Equal(t, core.EvidenceValidity{}, validity.Validity,
		"an impossible interval is cleared rather than believed")

	// f2 and f3 are still real records with valid authority, so they do apply.
	require.Equal(t, []string{"f2", "f3"}, ids(s.Applicable(scopeA)))
}
