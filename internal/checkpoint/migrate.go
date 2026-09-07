package checkpoint

import (
	"encoding/json"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// migration rewrites one checkpoint document from the version it is keyed under to that version
// plus one. It works on raw bytes rather than on a decoded Checkpoint deliberately: the current
// struct describes only the CURRENT schema, so decoding an older document with it would silently
// discard exactly the fields a migration exists to carry forward.
type migration func(raw []byte) ([]byte, error)

// migrations is keyed by the version being migrated FROM. It is empty at SchemaVersion 1 because
// there is nothing older to migrate; the map exists so that the FIRST schema change is a map entry
// and not a redesign.
var migrations = map[int]migration{}

// versionProbe reads nothing but the version field, so that a document written by a newer plugin
// is rejected on its version rather than on the first field this build does not recognize.
type versionProbe struct {
	Version *int `json:"version"`
}

// Migrate brings raw up to SchemaVersion, applying migrations[N], migrations[N+1], … in order. A
// document already at SchemaVersion is returned unchanged.
//
// It never decodes the full document with the current struct before migrating.
func Migrate(raw []byte) ([]byte, error) {
	// Three distinct failures, three distinct messages. They all wrap core.ErrContract — so every
	// errors.Is check behaves identically — but a human reading a log needs to know which of the
	// three it was: a file that is not JSON, a JSON document with no version key, and a version
	// key holding a nonsensical value are three different corruptions with three different causes.
	var probe versionProbe
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("checkpoint: malformed JSON: %v: %w", err, core.ErrContract)
	}
	if probe.Version == nil {
		return nil, fmt.Errorf("checkpoint: missing version: %w", core.ErrContract)
	}

	v := *probe.Version
	switch {
	case v < 1:
		return nil, fmt.Errorf("checkpoint: invalid version %d (must be at least 1): %w", v, core.ErrContract)
	case v > SchemaVersion:
		return nil, fmt.Errorf(
			"checkpoint: version %d written by a newer plugin (max %d): %w",
			v, SchemaVersion, core.ErrContract)
	}

	return migrateTo(raw, SchemaVersion)
}

// migrateTo applies migrations[v], migrations[v+1], … until raw reaches target. It is split out of
// Migrate so the stepping can be tested against a temporary migration table: at SchemaVersion 1 the
// real table is empty, so without this seam the loop below would first run in production, on the
// day the first real migration ships, against a user's only copy of their session.
//
// The caller has already validated raw's version, so every rejection here is about the migration
// TABLE rather than about the document.
func migrateTo(raw []byte, target int) ([]byte, error) {
	var probe versionProbe
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Version == nil {
		return nil, fmt.Errorf("checkpoint: missing version: %w", core.ErrContract)
	}
	v := *probe.Version

	// Termination proof, in full: the loop runs only while v < target, and every iteration either
	// returns or raises v by at least 1, because the advance check below rejects any migration
	// whose output version is not greater than the one it was handed. So the loop runs at most
	// target-v times. A second bound — a step counter capped at some constant — would add nothing
	// to that proof, and would subtract from it: whatever constant it held would eventually be
	// smaller than a legitimate chain, and the first thing it could ever do is reject one.
	out := raw
	for v < target {
		m, ok := migrations[v]
		if !ok {
			return nil, fmt.Errorf(
				"checkpoint: no migration registered from version %d to %d: %w",
				v, v+1, core.ErrContract)
		}
		next, err := m(out)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: migrate v%d: %w", v, err)
		}
		out = next

		var after versionProbe
		if err := json.Unmarshal(out, &after); err != nil || after.Version == nil {
			return nil, fmt.Errorf(
				"checkpoint: migration from version %d produced no version: %w", v, core.ErrContract)
		}
		if *after.Version <= v {
			return nil, fmt.Errorf(
				"checkpoint: migration from version %d did not advance the version: %w",
				v, core.ErrContract)
		}
		v = *after.Version
	}
	return out, nil
}
