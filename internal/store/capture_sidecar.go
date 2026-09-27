package store

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// CaptureSidecarVersion versions the observation capture sidecar independently of every frozen
// record. ToolUseRecord's wire shape is reproduced byte for byte by a checked-in golden and may not
// grow a field; this is the additive record that carries what that shape has no slot for — the
// permitted host payload, the fidelity it was captured at, the capture error that degraded it, and
// the transform/hash versions those were decided under.
const CaptureSidecarVersion = 1

// captureSidecarDir is the sidecar tree's name under <root>/.qompack/records/. It is deliberately
// NOT under objects/: the store's GC walks objects by reachability from index roots, and evidence
// about a delivery must not be collectable merely because no index record points at it yet — the
// window between a durable capture and its published reference is exactly the crash this record
// exists to survive.
const captureSidecarDir = "captures"

// captureSidecarShardWidth is how many leading hex characters of an observation id name the shard
// directory, so a long-lived project does not accumulate one flat directory of millions of files.
const captureSidecarShardWidth = 2

// captureSidecarHashDomain separates the sidecar payload digest from every other hash in the
// store, so a retention root declared for captured evidence can never be confused with a chunk or
// a Merkle root that happens to cover the same bytes. internal/core owns the shared registry and is
// not this package's to extend, so the domain is declared at its single use site.
const captureSidecarHashDomain = "qompack.capture.v1"

// captureSidecarIDHexLen is the hex width of a core.ObservationID's digest, the only shape that may
// name a file in this tree.
const captureSidecarIDHexLen = 2 * len(core.Hash{})

// CaptureSidecar is one delivery's durable capture record, keyed by its observation identity.
//
// Absent is absent: HostFields lists the top-level keys the permitted payload actually carried, so
// a reader can tell a host field the payload omitted from one the host sent empty. Bytes is the
// permitted payload verbatim, which is what makes unknown host fields survive a round trip through
// a build that does not name them.
type CaptureSidecar struct {
	Version       int                  `json:"v"`
	ObservationID core.ObservationID   `json:"observation_id"`
	Session       core.SessionID       `json:"session"`
	Arrival       uint64               `json:"arrival"`
	Op            string               `json:"op"`
	TS            core.UnixMilli       `json:"ts"`
	Delivery      string               `json:"delivery"`
	Admission     string               `json:"admission"`
	SourceFormat  string               `json:"source_format"`
	PolicyVersion string               `json:"policy"`
	HashVersion   string               `json:"hash"`
	Fidelity      core.Fidelity        `json:"fidelity"`
	Outcome       core.EvidenceOutcome `json:"outcome"`
	CaptureError  core.CaptureError    `json:"capture_error,omitempty"`
	Redacted      bool                 `json:"redacted"`
	Truncated     bool                 `json:"truncated"`
	SourceBytes   int                  `json:"source_bytes,omitempty"`
	HostFields    []string             `json:"host_fields,omitempty"`
	Bytes         []byte               `json:"bytes,omitempty"`
	// ToolUseID and Root are the verified reference this observation was published as, filled in by
	// LinkCaptureReference after the index record lands. Published says the join was made; a sidecar
	// with Published false is a durable capture with no reference yet, which is the exact state a
	// crash between publication order's first two stages leaves behind.
	ToolUseID core.ToolUseID `json:"tool_use_id,omitempty"`
	Root      core.Hash      `json:"root"`
	Published bool           `json:"published"`
	// BytesHash is the domain-separated digest of Bytes, zero when no bytes were retained. It is
	// what CaptureSidecarPath's caller registers as a retention root.
	BytesHash core.Hash `json:"bytes_hash"`
	// Unknown carries every key a future writer added that this build does not name, so an older
	// reader that rewrites a sidecar cannot silently drop a newer writer's fields. It is never
	// populated from a payload — only from the sidecar record itself.
	Unknown map[string]json.RawMessage `json:"-"`
}

// captureSidecarKeys is the set of names MarshalJSON emits itself; anything else found on disk is
// a future writer's field and is preserved through Unknown.
var captureSidecarKeys = map[string]bool{
	"v": true, "observation_id": true, "session": true, "arrival": true, "op": true, "ts": true,
	"delivery": true, "admission": true, "source_format": true, "policy": true, "hash": true,
	"fidelity": true, "outcome": true, "capture_error": true, "redacted": true, "truncated": true,
	"source_bytes": true, "host_fields": true, "bytes": true, "bytes_hash": true,
	"tool_use_id": true, "root": true, "published": true,
}

type captureSidecarWire CaptureSidecar

// MarshalJSON emits the named fields and then every preserved unknown key. A future writer's field
// therefore survives a read/modify/write cycle performed by this build.
func (c CaptureSidecar) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(captureSidecarWire(c))
	if err != nil {
		return nil, err
	}
	if len(c.Unknown) == 0 {
		return encoded, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &merged); err != nil {
		return nil, err
	}
	for k, v := range c.Unknown {
		if !captureSidecarKeys[k] {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

// UnmarshalJSON is MarshalJSON's inverse, collecting unrecognized keys into Unknown.
func (c *CaptureSidecar) UnmarshalJSON(b []byte) error {
	var w captureSidecarWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	var unknown map[string]json.RawMessage
	for k, v := range all {
		if captureSidecarKeys[k] {
			continue
		}
		if unknown == nil {
			unknown = map[string]json.RawMessage{}
		}
		unknown[k] = v
	}
	*c = CaptureSidecar(w)
	c.Unknown = unknown
	return nil
}

// CaptureSidecarPath returns the on-disk location of id's sidecar. The identity is validated as a
// 64-character hex digest, with or without the canonical "sha256:" prefix, precisely because it
// becomes a path element: an observation id that is not a digest cannot name a file here.
func CaptureSidecarPath(projectRoot string, id core.ObservationID) (string, error) {
	name := strings.TrimPrefix(string(id), "sha256:")
	if len(name) != captureSidecarIDHexLen || name != strings.ToLower(name) {
		return "", fmt.Errorf("%w: observation id is not a digest", core.ErrContract)
	}
	if _, err := hex.DecodeString(name); err != nil {
		return "", fmt.Errorf("%w: observation id is not a digest", core.ErrContract)
	}
	return filepath.Join(paths.Of(projectRoot).Records, captureSidecarDir,
		name[:captureSidecarShardWidth], name+".json"), nil
}

// WriteCaptureSidecar persists sc atomically and declares it to GC as an evidence retention root.
//
// It is idempotent by construction: the path is derived from the observation identity, so a
// redelivered delivery rewrites its own record rather than creating a second one. The write happens
// BEFORE any reference or frontier record, which is the first of publication order's three stages —
// a reference that named a capture this call had not yet made durable would be a published handle
// to an unavailable dependency after a crash between the two.
func WriteCaptureSidecar(projectRoot string, sc CaptureSidecar) error {
	if sc.ObservationID == "" {
		return fmt.Errorf("%w: a capture sidecar needs an observation identity", core.ErrContract)
	}
	p, err := CaptureSidecarPath(projectRoot, sc.ObservationID)
	if err != nil {
		return err
	}
	sc.Version = CaptureSidecarVersion
	// A redelivery rewrites its own record. Whatever reference the first delivery already published
	// is carried forward: re-capturing the same bytes is not a reason to forget that they were
	// published, and forgetting it would turn a completed publication back into an open one.
	if prior, err := ReadCaptureSidecar(projectRoot, sc.ObservationID); err == nil && prior.Published {
		sc.ToolUseID, sc.Root, sc.Published = prior.ToolUseID, prior.Root, true
	}
	if len(sc.Bytes) != 0 {
		sc.BytesHash = core.HashBytes(captureSidecarHashDomain, sc.Bytes)
	}
	encoded, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		return err
	}
	if err := paths.WriteAtomic(p, encoded, 0o600); err != nil {
		return err
	}
	if sc.BytesHash.IsZero() {
		return nil
	}
	// The declaration is what makes the evidence non-collectable under the retention contract,
	// rather than relying on the sidecar simply living outside objects/. A GC that later learns to
	// manage records/ inherits the protection — and must make this declaration durable when it does:
	// today nothing can collect what it names, so it is appended without a sync (see
	// appendRetentionRootVolatile for why that is the whole argument).
	return appendRetentionRootVolatile(projectRoot, RetentionRoot{
		Hash:   sc.BytesHash,
		Class:  RetentionEvidence,
		Reason: "capture sidecar " + string(sc.ObservationID),
	})
}

// ReadCaptureSidecar reads back the sidecar for id. A missing sidecar is reported as such rather
// than as an empty record, because "nothing was captured" and "the capture is gone" are different
// answers and only one of them is a gap.
//
// The read is shared (paths.ReadFileShared). The same sidecar is replaced with paths.WriteAtomic
// by WriteCaptureSidecar and by LinkCaptureReference, and a lost-ACK duplicate reaches the
// daemon's ingest on two paths, live and drain, that no lock this reader takes orders. On Windows
// an ordinary handle would fail such a replace, and a read refused by one would drop the prior
// sidecar's Published record, which WriteCaptureSidecar would then write back as open
// (test/guards' sharedReaders).
func ReadCaptureSidecar(projectRoot string, id core.ObservationID) (CaptureSidecar, error) {
	p, err := CaptureSidecarPath(projectRoot, id)
	if err != nil {
		return CaptureSidecar{}, err
	}
	b, err := paths.ReadFileShared(p)
	if err != nil {
		return CaptureSidecar{}, err
	}
	var sc CaptureSidecar
	if err := json.Unmarshal(b, &sc); err != nil {
		return CaptureSidecar{}, fmt.Errorf("%w: capture sidecar unreadable", core.ErrContract)
	}
	if sc.Version > CaptureSidecarVersion {
		// A newer record is readable — the named fields are additive-only — but the reader says so
		// rather than presenting a partial view as complete.
		return sc, fmt.Errorf("%w: capture sidecar version %d is newer than %d",
			core.ErrDegraded, sc.Version, CaptureSidecarVersion)
	}
	return sc, nil
}

// CaptureReference is publication order's SECOND stage: the verified reference. It names the index
// record and the durable content root that a published observation rests on, and it is written into
// the sidecar the capture already made durable, so the join between an observation identity and the
// record that published it survives a crash in either direction.
//
// ToolUseRecord's own wire shape is reproduced byte for byte by a checked-in golden and may not
// grow a field; this is where that link lives instead.
type CaptureReference struct {
	ToolUseID core.ToolUseID
	Root      core.Hash
}

// LinkCaptureReference records ref against id's sidecar. It is idempotent — a redelivery writes the
// same values — and it refuses to link a sidecar that does not exist, because a reference to a
// capture this store cannot produce is exactly the dangling handle publication order forbids.
func LinkCaptureReference(projectRoot string, id core.ObservationID, ref CaptureReference) error {
	sc, err := ReadCaptureSidecar(projectRoot, id)
	if err != nil {
		return err
	}
	if sc.ToolUseID == ref.ToolUseID && sc.Root == ref.Root && sc.Published {
		return nil
	}
	sc.ToolUseID, sc.Root, sc.Published = ref.ToolUseID, ref.Root, true
	p, err := CaptureSidecarPath(projectRoot, id)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	return paths.WriteAtomic(p, encoded, 0o600)
}
