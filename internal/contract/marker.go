package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// markerPerm is the permission run/marker.json is written with: owner-only, like every other file
// under .qompack/.
const markerPerm = 0o600

// markerRecord is the on-disk shape of run/marker.json (task-4-spec.md's marker.go section):
// {"session":"<id>","ts":<unixMilli>}.
type markerRecord struct {
	Session core.SessionID `json:"session"`
	TS      core.UnixMilli `json:"ts"`
}

// MarkerPath returns <projectRoot>/.qompack/run/marker.json.
func MarkerPath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).Run, "marker.json")
}

// WriteMarker writes {"session": sess, "ts": now} to MarkerPath(projectRoot) via paths.WriteAtomic.
//
// It is a mutable one-record view, not an append-only artifact, so WriteAtomic is the correct
// primitive and .qompack/run/ is outside the §3.3 append-only set. It is called by the daemon's
// flush and checkpoint routes only — SessionEnd and PreCompact, the two terminal hooks — and by
// nothing else in this codebase; the session_start.fires assertion reads it and never clears it,
// because it is overwritten by the next terminal hook and the assertion only ever compares its
// recorded session id against the current one.
func WriteMarker(projectRoot string, sess core.SessionID, now core.UnixMilli) error {
	b, err := json.Marshal(markerRecord{Session: sess, TS: now})
	if err != nil {
		return fmt.Errorf("contract: marshalling marker: %w", err)
	}
	p := MarkerPath(projectRoot)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		return fmt.Errorf("contract: mkdir for %s: %w", p, err)
	}
	if err := paths.WriteAtomic(p, b, markerPerm); err != nil {
		return fmt.Errorf("contract: writing %s: %w", p, err)
	}
	return nil
}

// readMarker reads and parses MarkerPath(projectRoot). Any error — a missing file (the ordinary
// "no marker observed yet" case), a permission failure, or corrupt JSON — is reported to the
// caller, which treats every one of them as "no observation yet", never as a failure: a marker file
// is evidence of a PAST terminal hook firing, and its absence proves nothing about whether the host
// contract itself is broken until it has been absent across two sessions (§12.1).
func readMarker(projectRoot string) (markerRecord, error) {
	b, err := os.ReadFile(paths.Long(MarkerPath(projectRoot)))
	if err != nil {
		return markerRecord{}, err
	}
	var rec markerRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return markerRecord{}, err
	}
	return rec, nil
}
