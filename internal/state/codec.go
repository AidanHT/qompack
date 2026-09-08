package state

import (
	"encoding/json"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// SnapshotVersion is the version of the durable document [Set.Encode] writes. It versions the
// ENVELOPE — the document's shape and the set of record versions it may carry — independently of
// [RecordVersion], which versions a single record. A document is refused unless a reader for its
// exact version exists, so a format change means a new version with its own reader, never a
// reinterpretation of the old bytes.
const SnapshotVersion = 1

// Snapshot is the durable form of a [Set]: a version stamp and the records in admission order.
// It is exported so a persistence layer can name the shape it stores; the shape is JSON, since
// every other durable record in this repository is (§5).
type Snapshot struct {
	Version int      `json:"v"`
	Records []Record `json:"records"`
}

// Encode writes s as a version-stamped [Snapshot]. Records keep their admission order, so two
// encodes of the same set produce the same bytes and a diff of two snapshots is readable.
func (s *Set) Encode() ([]byte, error) {
	b, err := json.Marshal(Snapshot{Version: SnapshotVersion, Records: s.All()})
	if err != nil {
		return nil, fmt.Errorf("%w: encoding a state snapshot: %v", core.ErrContract, err)
	}
	return b, nil
}

// Decode reads a document written by [Set.Encode] and returns the set it describes, with lineage,
// conflicts and resolutions as they were left.
//
// It refuses, rather than guesses, whenever it cannot establish what the bytes mean: a document
// with no version is a legacy document and stays legacy-unknown; a version this build does not
// have a reader for is refused rather than read as the nearest one it does; a record without its
// own version is refused; and a duplicate id is refused, because silently keeping one of two
// records with the same identity is how history disappears. Every refusal wraps
// [core.ErrContract].
//
// What it does NOT refuse is a field whose VALUE this version cannot classify, because dropping
// the record would lose durable data written by a peer. Those are qualified instead, in the
// manner of [core.EvidenceEnvelope.Qualified]:
//
//   - an authority label outside core's six is retained verbatim and never becomes applicable, so
//     a future label can neither be lost nor mistaken for user authority;
//   - an unreadable dependency coverage reads as [DepCoverageUnknown], never as complete;
//   - an impossible validity interval is cleared rather than believed.
func Decode(b []byte) (*Set, error) {
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, fmt.Errorf("%w: state snapshot is not readable JSON: %v", core.ErrContract, err)
	}
	switch {
	case snap.Version == 0:
		return nil, fmt.Errorf("%w: state snapshot carries no version; an unversioned document stays unknown",
			core.ErrContract)
	case snap.Version != SnapshotVersion:
		return nil, fmt.Errorf("%w: state snapshot version %d has no reader in this build (this build writes %d)",
			core.ErrContract, snap.Version, SnapshotVersion)
	}

	s := NewSet()
	for i, r := range snap.Records {
		switch {
		case r.Version == 0:
			return nil, fmt.Errorf("%w: state record at position %d carries no version", core.ErrContract, i)
		case r.Version != RecordVersion:
			return nil, fmt.Errorf("%w: state record %q is version %d, which has no reader in this build (this build writes %d)",
				core.ErrContract, r.ID, r.Version, RecordVersion)
		case r.ID == "":
			return nil, fmt.Errorf("%w: state record at position %d has no id", core.ErrContract, i)
		}
		if err := s.admit(qualify(r)); err != nil {
			return nil, fmt.Errorf("%w: state snapshot repeats record id %q", core.ErrContract, r.ID)
		}
	}
	return s, nil
}

// qualify degrades the fields whose values this version cannot classify, without altering
// anything it can read. It never rewrites an authority label: core's rule is that an unknown
// authority is retained as unknown, and coercing it — in either direction — is the silent
// promotion invariant 7 forbids.
func qualify(r Record) Record {
	if !r.Dependencies.Coverage.Valid() {
		r.Dependencies.Coverage = DepCoverageUnknown
	}
	if !validInterval(r.Validity) {
		r.Validity = core.EvidenceValidity{}
	}
	return r
}
