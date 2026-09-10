// V5 §4.17 — the SP-21 admission extension, composed the way its composition root would compose it.
//
// internal/admission ships with no composition root: its five ports (Privacy, Capturer, Publisher,
// Parser, Resolver) have no production adapters, the feature switch is refused by
// internal/config's gate table, and the host allowlist is empty under B01. This row therefore
// lives in test/integration rather than test/e2e — no binary, daemon or hook reaches the pipeline
// — and it wires the ports to the REAL producers the plan names for them: SP-20's content-addressed
// store as capturer, publisher and resolver (store.PutBytes / GetRoot / RestoreOriginal), the real
// redact.CapturePolicy as the privacy check, the real config loader and gate table as the switch,
// and the real contract register as the enablement record. The one adapter with no production
// counterpart anywhere on the tree is the Parser; the test supplies a minimal JSON parser for a
// Qompack-owned result and says so, because no real one exists to stand in for.
//
// Retired clauses this row must NOT assert: nothing here claims a host delivered, accepted or
// displayed a replaced result (B01 keeps the installed host unobserved); nothing here proves a real
// privacy DENIAL end to end, because no shipped policy on this tree ever answers OutcomeDenied —
// the denial rule is asserted at the Decide level only and recorded as such; and no handle is
// resolved against SP-13/M2 authorization, because that adapter does not exist. Resolution here is
// the real store's own read-back.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/admission"
	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x17Path is the store path every captured result is filed under.
const x17Path = "results/v5-x17.json"

// x17NewResultKey is the dotted config leaf behind SP-21's feature switch, as migration.go gates it.
const x17NewResultKey = "runtime.migration.replacement.newResult"

// x17OptInJSON is a project config that tries to turn admission on. The gate table must refuse it.
const x17OptInJSON = `{"runtime":{"migration":{"replacement":{"newResult":true}}}}`

// x17ForeignMarker is what another hook would stamp on a result it already transformed.
const x17ForeignMarker = "other-hook.transform/v1"

// x17Margin is the predeclared minimum retention ratio for the quality comparison. The package
// declares no default on purpose (admission.Predeclare), so the test must.
const x17Margin = 0.95

// x17Tasks is the held-out task count fed to the quality comparison. Small on purpose: this row
// proves the arithmetic and the enablement rule, not a statistical claim (V5-VERIFY §5).
const x17Tasks = 20

// x17OwnedTarget is the schema and version of a Qompack-owned result — the first supported surface.
var x17OwnedTarget = admission.Target{Schema: "qompack.result", Version: "1"}

// x17HostTarget is the one entry of a tiny host allowlist. It is admitted only by exact match.
var x17HostTarget = admission.Target{Schema: "host.tool-result", Version: "1"}

// x17Result is the shape of a Qompack-owned delivered result as this row's parser reads it. Every
// field maps onto one admission.Meaning field so invariant 7's list can be checked end to end.
type x17Result struct {
	Status      string   `json:"status"`
	Stderr      string   `json:"stderr"`
	Diagnostics []string `json:"diagnostics"`
	Interrupted bool     `json:"interrupted"`
	Media       bool     `json:"media"`
	InputCount  int      `json:"input_count"`
	OutputCount int      `json:"output_count"`
	Signature   string   `json:"signature"`
	Source      string   `json:"source"`
	Displayed   string   `json:"displayed"`
}

// x17Payload encodes a result the way a Qompack-owned producer would deliver it.
func x17Payload(t *testing.T, r x17Result) []byte {
	t.Helper()
	b, err := json.Marshal(r)
	require.NoError(t, err)
	return b
}

// x17Failure is a representative failed Bash result: a status, stderr, two diagnostics, counts, a
// failure signature and both halves of the displayed meaning.
//
// The stderr carries a timestamp on purpose, and a DISTINCT one per payload (stamp is the seconds
// field; every call site passes its own). The default canonicalizer strips timestamps, so the
// stored canonical bytes differ from the delivered ones and the store must keep a real recovery
// record for a KeepRaw put. Two real store behaviours found while authoring this row make both
// halves necessary:
//
//   - With NO volatile token the bytes are unchanged by canonicalization: the put reports exact
//     with no side record, and RestoreOriginal, which cannot tell "no record because nothing
//     changed" from "no record was asked for", labels the same bytes canonical on read-back.
//   - With the SAME volatile token at the same offset in two different payloads, both roots
//     produce an identical delta list; putSideRecord content-addresses the side record over that
//     list alone and returns the existing one, whose declared base is the FIRST root. The second
//     root's put still reports exact, and its RestoreOriginal then fails with ErrDeltaCorrupt
//     ("declares base X, not Y"). That is recorded as a store finding in this row's disposition;
//     the distinct stamps keep this test on the seam it owns rather than on that defect.
func x17Failure(stamp int, signature, displayed string) x17Result {
	return x17Result{
		Status:      "exit 1",
		Stderr:      fmt.Sprintf("go: build failed at 2026-09-09T10:00:%02dZ", stamp),
		Diagnostics: []string{"pkg/a.go:12: undefined: x", "pkg/b.go:4: unused import"},
		InputCount:  3,
		OutputCount: 1,
		Signature:   signature,
		Source:      "toolu_v5x17_" + signature,
		Displayed:   displayed,
	}
}

// x17Rig is one disposable project with the real store and the real capture policy over it.
type x17Rig struct {
	P      *testutil.Project
	Store  *store.FSStore
	Policy func([]byte) (core.CaptureDecision, error)
}

// x17Open composes the real store (testutil's default deps are internal/store's own) and compiles
// the real configured privacy rule set over a fresh project.
func x17Open(t *testing.T) *x17Rig {
	t.Helper()
	p := testutil.NewProject(t)
	s := p.Store(t)
	fs, ok := s.(*store.FSStore)
	require.True(t, ok, "store.Open returns the filesystem store; RestoreOriginal lives on it")
	policy, err := redact.CapturePolicy(p.Cfg)
	require.NoError(t, err, "the shipped rule set must compile")
	return &x17Rig{P: p, Store: fs, Policy: policy}
}

// Ports binds the pipeline's ports to this rig's real producers. keepRaw is store.PutOptions.KeepRaw
// verbatim: with it the store retains a recovery record (FidelityExact or FidelityFull); without it
// the store itself reports FidelityCanonical, which is the real "not recoverable" answer.
func (r *x17Rig) Ports(keepRaw bool) admission.Ports {
	return admission.Ports{
		Privacy: x17Privacy{policy: r.Policy},
		Capture: x17Capturer{s: r.Store, cfg: r.P.Cfg, keepRaw: keepRaw},
		Publish: x17Publisher{s: r.Store},
		Parse:   x17Parser{},
		Resolve: x17Resolver{s: r.Store},
	}
}

// Objects is the store's current object count, the observable that says whether a capture ran.
func (r *x17Rig) Objects(t *testing.T) int {
	t.Helper()
	st, err := r.Store.Stats(context.Background())
	require.NoError(t, err)
	return st.Objects
}

// x17Privacy adapts the real redact.CapturePolicy to admission.Privacy exactly as
// internal/daemon's admitDelivery classifies the same decision: OutcomeOK permits, OutcomeDenied
// is a decision to withhold, and every other outcome is "policy could not decide", which is an
// error here and never a denial.
type x17Privacy struct {
	policy func([]byte) (core.CaptureDecision, error)
}

func (x x17Privacy) Permits(_ context.Context, payload []byte) (bool, error) {
	d, err := x.policy(payload)
	if err != nil {
		return false, err
	}
	switch d.Outcome {
	case core.OutcomeOK:
		return true, nil
	case core.OutcomeDenied:
		return false, nil
	default:
		return false, fmt.Errorf("%w: capture policy answered %q", core.ErrDegraded, d.Outcome)
	}
}

// x17Capturer is the composition-root adapter the admission package documents for its Capturer
// port: store.PutBytes, with the store's own fidelity carried across unchanged.
type x17Capturer struct {
	s       store.Store
	cfg     config.Config
	keepRaw bool
}

func (x x17Capturer) Capture(ctx context.Context, payload []byte) (admission.Capture, error) {
	res, err := x.s.PutBytes(ctx, payload, store.PutOptions{
		Tool:    "Bash",
		Path:    x17Path,
		Canon:   canon.OptionsFrom(x.cfg.Store.Canonicalize, x.keepRaw),
		KeepRaw: x.keepRaw,
	})
	if err != nil {
		return admission.Capture{}, err
	}
	return admission.Capture{
		Handle:    res.Root.Hash.String(),
		Fidelity:  admission.Fidelity(res.Fidelity),
		Truncated: res.Truncated,
		Redacted:  res.Redacted,
	}, nil
}

// x17Publisher verifies a capture against the store that is supposed to hold it: the index is
// flushed, the root is indexed, and every chunk the root names is on disk.
type x17Publisher struct {
	s store.Store
}

func (x x17Publisher) VerifyPublished(ctx context.Context, c admission.Capture) error {
	h, err := core.ParseHash(c.Handle)
	if err != nil {
		return err
	}
	if err := x.s.Flush(ctx); err != nil {
		return err
	}
	root, err := x.s.GetRoot(ctx, h)
	if err != nil {
		return err
	}
	for _, ch := range root.Chunks {
		if !x.s.Has(ch.Hash) {
			return fmt.Errorf("%w: chunk %s of root %s is not on disk", core.ErrNotFound, ch.Hash.Short(), h.Short())
		}
	}
	return nil
}

// x17Parser is this row's parser for a Qompack-owned result. There is no production parser on the
// tree — the port is unwired at every composition root — so this is the one adapter here that is
// not a shipped producer, and the row's disposition says so.
type x17Parser struct{}

func (x17Parser) Parse(_ context.Context, d admission.Delivery) (admission.Meaning, error) {
	dec := json.NewDecoder(bytes.NewReader(d.Payload))
	dec.DisallowUnknownFields()
	var r x17Result
	if err := dec.Decode(&r); err != nil {
		return admission.Meaning{}, err
	}
	return admission.Meaning{
		Status:      r.Status,
		Stderr:      r.Stderr,
		Diagnostics: r.Diagnostics,
		Interrupted: r.Interrupted,
		Media:       r.Media,
		InputCount:  r.InputCount,
		OutputCount: r.OutputCount,
		Signature:   r.Signature,
		Source:      admission.Source{ID: r.Source, Span: admission.Span{End: int64(len(d.Payload))}},
		Schema:      d.Target,
		Structured:  bytes.Clone(d.Payload),
		Displayed:   r.Displayed,
	}, nil
}

// x17Resolver resolves a handle by reading the archived original back from the real store. A root
// the store does not hold is unavailable; a store that cannot answer (closed, degraded) is
// uncertain, never denied.
type x17Resolver struct {
	s *store.FSStore
}

func (x x17Resolver) Resolve(ctx context.Context, handle string) (admission.HandleState, error) {
	h, err := core.ParseHash(handle)
	if err != nil {
		return admission.HandleUnavailable, nil
	}
	_, _, err = x.s.RestoreOriginal(ctx, h)
	switch {
	case err == nil:
		return admission.HandleResolvable, nil
	case errors.Is(err, core.ErrNotFound):
		return admission.HandleUnavailable, nil
	default:
		return admission.HandleUncertain, err
	}
}

// x17GateFromConfig builds the gate the way a composition root would: the switch is the loaded
// configuration's own value, the surface is Qompack-owned, and the allowlist is the shipped one.
func x17GateFromConfig(cfg config.Config) admission.Gate {
	return admission.Gate{
		Enabled: cfg.Runtime.Migration.Replacement.NewResult,
		Owned:   true,
		Allow:   admission.NewAllowlist(),
	}
}

// x17Admit runs one delivery through a pipeline over the given gate and ports.
func x17Admit(t *testing.T, g admission.Gate, ports admission.Ports, d admission.Delivery) admission.Record {
	t.Helper()
	rec, err := admission.NewPipeline(g, ports).Admit(context.Background(), d)
	require.NoError(t, err, "Admit reports refusals on the record, never as an error")
	return rec
}

// x17Owned is a fresh, unmarked, Qompack-owned delivery of payload with no baseline.
func x17Owned(payload []byte) admission.Delivery {
	return admission.Delivery{Target: x17OwnedTarget, Payload: payload}
}

// x17RequireRefused asserts the shape every refusal shares: pass-through, no handle, no mark, no
// form, no meaning, and the reason named.
func x17RequireRefused(t *testing.T, rec admission.Record, want admission.Reason) {
	t.Helper()
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome, "reason=%s stage=%s err=%v", rec.Reason, rec.Stage, rec.Err)
	require.Equal(t, want, rec.Reason, "stage=%s err=%v", rec.Stage, rec.Err)
	require.Empty(t, rec.Handle, "a refusal emits no handle")
	require.Empty(t, rec.Mark, "a refusal tells the caller to stamp nothing")
	require.Equal(t, admission.FormNone, rec.Form, "a refusal selects no representation")
	require.Empty(t, admission.Preserves(admission.Meaning{}, rec.Meaning),
		"a refusal must not hand the caller an actionable meaning")
	require.Equal(t, core.CoverageUnknown, rec.Coverage, "admission does not observe what the host does with a pass-through")
}

// x17ReadBaseline builds a baseline the way a caller legitimately can: by resolving the prior
// capsule's handle against a store and reading it back. A store that does not hold the root yields
// an UNVERIFIED baseline carrying only the id, because nothing was read.
func x17ReadBaseline(t *testing.T, s *store.FSStore, prior admission.Record, m admission.Meaning) admission.Baseline {
	t.Helper()
	h, err := core.ParseHash(prior.Handle)
	require.NoError(t, err)
	b, fid, err := s.RestoreOriginal(context.Background(), h)
	if err != nil {
		return admission.Baseline{ID: prior.Handle}
	}
	require.NotEmpty(t, b, "a readable baseline has bytes")
	return admission.VerifyBaseline(prior.Handle, m.Signature, m.Schema, admission.Fidelity(fid))
}

// x17OptInRefused loads a project whose config file turns the switch on and returns what the real
// loader made of it: the merged value and the warning that explains the fallback.
func x17OptInRefused(t *testing.T) (config.Config, []config.Warning) {
	t.Helper()
	p := testutil.NewProject(t, testutil.WithConfig(x17OptInJSON))
	cfg, _, warns, err := config.Load(config.Env{ProjectRoot: p.Root, HomeDir: p.Home(), Getenv: p.Getenv})
	require.NoError(t, err, "Load never fails for a value it can fall back from")
	return cfg, warns
}

// x17FieldNames lists a struct type's exported field names, lower-cased.
func x17FieldNames(v any) []string {
	typ := reflect.TypeOf(v)
	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		names = append(names, strings.ToLower(typ.Field(i).Name))
	}
	return names
}

// TestV5_AdmissionExtension is V5-VERIFY §4.17.
//
// Four bullets, four subtests, with the disabled-is-disabled rule first because every later arm
// runs on an in-process gate the shipped configuration cannot produce. The negative controls are
// real switches: the store's own KeepRaw=false fidelity, a closed store, a store that never
// published the object, a config file that opts in and is refused by the gate table, and the
// marker that makes a redelivery bypass.
func TestV5_AdmissionExtension(t *testing.T) {
	r := x17Open(t)

	t.Run("disabled_admission_is_recorded_as_disabled_never_passed", func(t *testing.T) {
		gate := x17GateFromConfig(r.P.Cfg)
		require.False(t, gate.Enabled, "the shipped default of %s is false", x17NewResultKey)

		before := r.Objects(t)
		payload := x17Payload(t, x17Failure(1, "sig-disabled", "build failed"))
		rec := x17Admit(t, gate, r.Ports(true), x17Owned(payload))
		x17RequireRefused(t, rec, admission.ReasonDisabled)
		require.Equal(t, admission.StageNone, rec.Stage, "no stage ran, so the record must not blame one")
		require.NoError(t, rec.Err)
		require.Equal(t, before, r.Objects(t),
			"a disabled pipeline must not archive a second copy of every result on its way to refusing")

		// The only way to turn the switch on is the reviewed commit that flips the gate: a config
		// file that opts in falls back to the default and says so.
		cfg, warns := x17OptInRefused(t)
		require.False(t, cfg.Runtime.Migration.Replacement.NewResult,
			"the loader must refuse %s=true while the M4 gate is pending", x17NewResultKey)
		var refused bool
		for _, w := range warns {
			if w.Key == x17NewResultKey && !w.Deprecated {
				refused = true
				require.Contains(t, w.Message, "using default", "the refusal is a fallback, not silence")
			}
		}
		require.True(t, refused, "the loader must warn at %s; warnings=%+v", x17NewResultKey, warns)
		require.Equal(t, admission.ReasonDisabled, x17Admit(t, x17GateFromConfig(cfg), r.Ports(true), x17Owned(payload)).Reason,
			"a pipeline built from the refused config records disabled")

		var gated bool
		for _, g := range config.MigrationGates() {
			if g.Key == x17NewResultKey {
				gated = true
				require.False(t, g.Passed, "gate %q (%s) must be pending in this build", g.Gate, g.Owner)
				require.Equal(t, "SP-21", g.Owner)
			}
		}
		require.True(t, gated, "%s must be in the gate table", x17NewResultKey)

		// The capability register is the enablement record. It must say disabled and documented —
		// never verified in target, which is the status a pass would carry.
		cap, ok := contract.DefaultCapabilityRegister().Get(contract.CapNewResultReplacement)
		require.True(t, ok)
		require.False(t, cap.Enabled, "new-result replacement must be recorded disabled")
		require.Equal(t, contract.StatusDocumented, cap.Status)
		require.NotEqual(t, contract.StatusVerifiedInTarget, cap.Status, "disabled is never recorded as passed")
		require.Contains(t, cap.Fallback, "pass-through")
		require.True(t, admission.NewAllowlist().Empty(), "the shipped host allowlist is empty")
	})

	// Every arm below runs on an in-process gate. The loaded configuration cannot produce Enabled
	// (the previous subtest proves it), so this is the authoring gate the package's own tests use;
	// nothing here enables anything outside this process.
	owned := admission.Gate{Enabled: true, Owned: true}

	t.Run("fresh_owned_result_transforms_once_then_envelope_bypasses", func(t *testing.T) {
		want := x17Failure(2, "sig-A", "the build failed with two errors")
		payload := x17Payload(t, want)
		original := bytes.Clone(payload)
		before := r.Objects(t)

		rec := x17Admit(t, owned, r.Ports(true), x17Owned(payload))
		require.Equal(t, admission.OutcomeTransform, rec.Outcome, "reason=%s stage=%s err=%v", rec.Reason, rec.Stage, rec.Err)
		require.Equal(t, admission.ReasonAdmitted, rec.Reason)
		require.Equal(t, admission.StageNone, rec.Stage)
		require.NoError(t, rec.Err)
		require.NotEmpty(t, rec.Handle, "an admitted record carries the resolvable handle")
		require.Equal(t, admission.MarkerProducer, rec.Mark, "the caller must stamp admission's own marker")
		require.Equal(t, admission.FormCapsule, rec.Form, "a self-contained capsule precedes every delta")
		require.Equal(t, admission.ResetNoBaseline, rec.Reset)
		require.Empty(t, rec.Base, "a capsule names no baseline")
		require.Equal(t, core.CoverageArchiveOnly, rec.Coverage)
		require.True(t, rec.Fidelity.Recoverable(), "fidelity=%s", rec.Fidelity)
		require.Equal(t, admission.HandleResolvable, rec.HandleState)
		require.Equal(t, original, payload, "admission never rewrites the delivered bytes")
		require.Greater(t, r.Objects(t), before, "the capture really landed in the store")

		// The handle reads back the delivered bytes from the real store, under its fidelity.
		h, err := core.ParseHash(rec.Handle)
		require.NoError(t, err)
		got, fid, err := r.Store.RestoreOriginal(context.Background(), h)
		require.NoError(t, err)
		require.Equal(t, admission.Fidelity(fid), rec.Fidelity, "the record's fidelity is the store's")
		require.Equal(t, original, got, "the archived original is byte-identical to what was delivered")

		// Invariant 7: structured and displayed meaning survive together onto the record.
		parsed, err := x17Parser{}.Parse(context.Background(), x17Owned(payload))
		require.NoError(t, err)
		require.Empty(t, admission.Preserves(parsed, rec.Meaning), "every Meaning field must survive")
		require.Equal(t, want.Status, rec.Meaning.Status)
		require.Equal(t, want.Stderr, rec.Meaning.Stderr)
		require.Equal(t, want.Diagnostics, rec.Meaning.Diagnostics)
		require.Equal(t, want.Displayed, rec.Meaning.Displayed)
		require.Equal(t, original, rec.Meaning.Structured)

		// A redelivery carrying admission's own marker is output this pipeline already produced:
		// it bypasses before capture, so the store does not grow by one archive per redelivery.
		after := r.Objects(t)
		again := x17Owned(payload)
		again.Envelope = admission.Envelope{Markers: []string{rec.Mark}}
		bypass := x17Admit(t, owned, r.Ports(true), again)
		x17RequireRefused(t, bypass, admission.ReasonAlreadyProcessed)
		require.Equal(t, []string{admission.MarkerProducer}, bypass.Observed, "the observed chain stays on the record")
		require.Equal(t, after, r.Objects(t), "a bypass captures nothing")

		// Another hook's transformation is a different fact with the same refusal.
		foreign := x17Owned(payload)
		foreign.Envelope = admission.Envelope{Markers: []string{x17ForeignMarker}}
		fb := x17Admit(t, owned, r.Ports(true), foreign)
		x17RequireRefused(t, fb, admission.ReasonForeignTransform)
		require.Equal(t, []string{x17ForeignMarker}, fb.Observed)
		require.Equal(t, after, r.Objects(t))

		// Ours outranks foreign when both are present, whatever order the adapter read them in.
		both := x17Owned(payload)
		both.Envelope = admission.Envelope{Markers: []string{x17ForeignMarker, rec.Mark}}
		require.Equal(t, admission.ReasonAlreadyProcessed, x17Admit(t, owned, r.Ports(true), both).Reason)

		// A repeated FRESH delivery of the same bytes is idempotent: the content-addressed store
		// answers the same handle and grows by nothing.
		repeat := x17Admit(t, owned, r.Ports(true), x17Owned(payload))
		require.Equal(t, admission.ReasonAdmitted, repeat.Reason)
		require.Equal(t, rec.Handle, repeat.Handle, "same bytes, same handle")
		require.Equal(t, after, r.Objects(t), "a duplicate capture deduplicates to nothing new")

		// The tiny host allowlist: one exact entry, admitted by exact match and nothing else.
		host := admission.Gate{Enabled: true, Allow: admission.NewAllowlist(x17HostTarget)}
		hostRec := x17Admit(t, host, r.Ports(true), admission.Delivery{Target: x17HostTarget, Payload: payload})
		require.Equal(t, admission.ReasonAdmitted, hostRec.Reason, "the exact allowlisted target is admitted")
		require.Equal(t, x17HostTarget, hostRec.Target)
		require.Equal(t, x17HostTarget, hostRec.Meaning.Schema)

		bumped := admission.Target{Schema: x17HostTarget.Schema, Version: "2"}
		mark := r.Objects(t)
		x17RequireRefused(t, x17Admit(t, host, r.Ports(true), admission.Delivery{Target: bumped, Payload: payload}),
			admission.ReasonUnknownTarget)
		require.Equal(t, mark, r.Objects(t), "an unknown version is refused before anything is captured")

		shipped := admission.Gate{Enabled: true, Allow: admission.NewAllowlist()}
		x17RequireRefused(t, x17Admit(t, shipped, r.Ports(true), admission.Delivery{Target: x17HostTarget, Payload: payload}),
			admission.ReasonUnknownTarget)
	})

	t.Run("durable_capture_and_resolution_precede_replacement", func(t *testing.T) {
		payload := x17Payload(t, x17Failure(3, "sig-B", "first delivery"))

		// NEGATIVE CONTROL (real switch): the store's own KeepRaw=false answer is canonical-only
		// fidelity. The stage ran and retained something, and what it retained is not the original,
		// so replacement is forbidden and no handle is emitted. The payload is this arm's own,
		// because of what the second half proves.
		before := r.Objects(t)
		canonicalPayload := x17Payload(t, x17Failure(4, "sig-canonical", "archived without a recovery record"))
		canonical := x17Admit(t, owned, r.Ports(false), x17Owned(canonicalPayload))
		x17RequireRefused(t, canonical, admission.ReasonCaptureFailed)
		require.Equal(t, admission.StageCapture, canonical.Stage)
		require.ErrorIs(t, canonical.Err, admission.ErrUnrecoverableCapture)
		require.Greater(t, r.Objects(t), before, "the canonical bytes were archived; they are just not the original")

		// Fidelity is a property of the root's FIRST put: a later KeepRaw put of the same bytes
		// deduplicates onto the record that has no recovery record, and the store keeps saying
		// canonical rather than promoting it. Redelivery cannot launder an unrecoverable capture.
		relaundered := x17Admit(t, owned, r.Ports(true), x17Owned(canonicalPayload))
		x17RequireRefused(t, relaundered, admission.ReasonCaptureFailed)
		require.ErrorIs(t, relaundered.Err, admission.ErrUnrecoverableCapture)

		// NEGATIVE CONTROL (real switch): a closed store cannot capture at all.
		closed := x17Open(t)
		require.NoError(t, closed.Store.Close())
		dead := x17Admit(t, owned, closed.Ports(true), x17Owned(payload))
		x17RequireRefused(t, dead, admission.ReasonCaptureFailed)
		require.Equal(t, admission.StageCapture, dead.Stage)
		require.ErrorIs(t, dead.Err, core.ErrDegraded)

		// Publication is verified against the store that must hold the object. A store that never
		// published it refuses at the publication stage, after a capture that succeeded elsewhere.
		empty := x17Open(t)
		split := r.Ports(true)
		split.Publish = x17Publisher{s: empty.Store}
		unpublished := x17Admit(t, owned, split, x17Owned(payload))
		x17RequireRefused(t, unpublished, admission.ReasonPublicationUnverified)
		require.Equal(t, admission.StagePublication, unpublished.Stage)
		require.ErrorIs(t, unpublished.Err, core.ErrNotFound)

		// Resolution runs under the resolver's current view. A handle the resolver's store does
		// not hold blocks the transform and stays visible as unavailable; a resolver that cannot
		// answer stays visible as uncertain. Neither is a denial.
		unresolved := r.Ports(true)
		unresolved.Resolve = x17Resolver{s: empty.Store}
		blocked := x17Admit(t, owned, unresolved, x17Owned(payload))
		x17RequireRefused(t, blocked, admission.ReasonHandleUnresolvable)
		require.Equal(t, admission.StageResolution, blocked.Stage)
		require.Equal(t, admission.HandleUnavailable, blocked.HandleState)
		require.ErrorIs(t, blocked.Err, admission.ErrHandleUnresolvable)

		uncertain := r.Ports(true)
		uncertain.Resolve = x17Resolver{s: closed.Store}
		unsure := x17Admit(t, owned, uncertain, x17Owned(payload))
		x17RequireRefused(t, unsure, admission.ReasonHandleUnresolvable)
		require.Equal(t, admission.HandleUncertain, unsure.HandleState)
		require.NotEqual(t, admission.OutcomeDeny, unsure.Outcome, "a transport failure is not an authorization decision")

		// The admitted path, then the baseline rules over it.
		first := x17Admit(t, owned, r.Ports(true), x17Owned(payload))
		require.Equal(t, admission.ReasonAdmitted, first.Reason, "stage=%s err=%v", first.Stage, first.Err)
		require.Equal(t, admission.FormCapsule, first.Form)
		require.Equal(t, admission.ResetNoBaseline, first.Reset)
		firstMeaning, err := x17Parser{}.Parse(context.Background(), x17Owned(payload))
		require.NoError(t, err)

		// A baseline read back from the real store supports a delta for a same-signature result.
		verified := x17ReadBaseline(t, r.Store, first, firstMeaning)
		require.True(t, verified.Verified())
		next := x17Owned(x17Payload(t, x17Failure(5, "sig-B", "second delivery, same failure")))
		next.Baseline = verified
		delta := x17Admit(t, owned, r.Ports(true), next)
		require.Equal(t, admission.ReasonAdmitted, delta.Reason)
		require.Equal(t, admission.FormDelta, delta.Form)
		require.Equal(t, first.Handle, delta.Base, "the delta names the capsule it is relative to")
		require.Equal(t, admission.ResetNone, delta.Reset)

		// The same baseline merely NAMED — a prior delivery record, not a read — resets to a capsule.
		named := next
		named.Baseline = admission.Baseline{ID: first.Handle, Signature: firstMeaning.Signature, Schema: firstMeaning.Schema, Fidelity: first.Fidelity}
		require.False(t, named.Baseline.Verified())
		require.Equal(t, admission.ResetUnverified, x17Admit(t, owned, r.Ports(true), named).Reset)

		// A baseline that cannot be read back from the resolver's store is not available, and the
		// caller cannot verify it: the delivery resets.
		unavailable := next
		unavailable.Baseline = x17ReadBaseline(t, empty.Store, first, firstMeaning)
		require.False(t, unavailable.Baseline.Verified())
		gone := x17Admit(t, owned, r.Ports(true), unavailable)
		require.Equal(t, admission.FormCapsule, gone.Form)
		require.Equal(t, admission.ResetUnverified, gone.Reset)

		// A changed failure signature resets.
		changedSig := x17Owned(x17Payload(t, x17Failure(6, "sig-C", "a different failure")))
		changedSig.Baseline = verified
		sigRec := x17Admit(t, owned, r.Ports(true), changedSig)
		require.Equal(t, admission.FormCapsule, sigRec.Form)
		require.Equal(t, admission.ResetSignatureChanged, sigRec.Reset)

		// A changed parser schema/version resets, even with the signature unchanged.
		changedSchema := next
		changedSchema.Target = admission.Target{Schema: x17OwnedTarget.Schema, Version: "2"}
		schemaRec := x17Admit(t, owned, r.Ports(true), changedSchema)
		require.Equal(t, admission.ReasonAdmitted, schemaRec.Reason, "an owned result is admitted at any version")
		require.Equal(t, admission.FormCapsule, schemaRec.Form)
		require.Equal(t, admission.ResetSchemaChanged, schemaRec.Reset)

		// A baseline whose own capture is not recoverable is nothing dependable to be relative to.
		doubtful := next
		doubtful.Baseline = admission.VerifyBaseline(first.Handle, firstMeaning.Signature, firstMeaning.Schema, admission.FidelityCanonical)
		require.Equal(t, admission.ResetUncertainCapture, x17Admit(t, owned, r.Ports(true), doubtful).Reset)
	})

	t.Run("unsupported_content_passes_through_or_follows_privacy_policy", func(t *testing.T) {
		// A binary or multimodal payload selects no representation: captured, then passed through
		// with nothing actionable on the record.
		media := x17Failure(7, "sig-media", "an image")
		media.Media = true
		mediaPayload := x17Payload(t, media)
		mediaOriginal := bytes.Clone(mediaPayload)
		mediaRec := x17Admit(t, owned, r.Ports(true), x17Owned(mediaPayload))
		x17RequireRefused(t, mediaRec, admission.ReasonNoRepresentation)
		require.Equal(t, admission.StageSelection, mediaRec.Stage)
		require.Equal(t, mediaOriginal, mediaPayload, "the delivered bytes are untouched")

		// An unknown schema is refused before capture.
		before := r.Objects(t)
		unknown := admission.Delivery{Target: admission.Target{Schema: "unknown.schema", Version: "9"}, Payload: mediaPayload}
		x17RequireRefused(t, x17Admit(t, admission.Gate{Enabled: true}, r.Ports(true), unknown), admission.ReasonUnknownTarget)
		require.Equal(t, before, r.Objects(t))

		// A payload that does not parse under its declared schema passes through with the parser's
		// error preserved on the record.
		malformed := []byte(`{"status":5}`)
		parseRec := x17Admit(t, owned, r.Ports(true), x17Owned(malformed))
		x17RequireRefused(t, parseRec, admission.ReasonParseFailed)
		require.Equal(t, admission.StageParse, parseRec.Stage)
		require.Error(t, parseRec.Err, "the parse error is the audit diagnostic")
		require.Equal(t, x17OwnedTarget, parseRec.Target, "the declared target stays on the record")

		// The real capture policy refuses bytes that are not a JSON object as a host-contract
		// violation it cannot redact safely. That is "we could not ask policy", which passes the
		// original through and captures nothing — it is NOT a denial, and the record says which.
		mark := r.Objects(t)
		notJSON := []byte("not a json object at all")
		undecided := x17Admit(t, owned, r.Ports(true), x17Owned(notJSON))
		x17RequireRefused(t, undecided, admission.ReasonPolicyUnavailable)
		require.Equal(t, admission.StagePolicy, undecided.Stage)
		require.ErrorIs(t, undecided.Err, core.ErrContract)
		require.NotEqual(t, admission.OutcomeDeny, undecided.Outcome)
		require.Equal(t, mark, r.Objects(t), "an unanswerable privacy check persists nothing")

		// An unwired privacy port is the same fact with a different error.
		unwired := r.Ports(true)
		unwired.Privacy = nil
		noPolicy := x17Admit(t, owned, unwired, x17Owned(mediaPayload))
		x17RequireRefused(t, noPolicy, admission.ReasonPolicyUnavailable)
		require.ErrorIs(t, noPolicy.Err, admission.ErrPortsUnwired)

		// Denial is the frozen policy's answer to a privacy DECISION, and it outranks the switch:
		// a disabled build must not deliver bytes policy refused. No shipped policy on this tree
		// produces that decision, so this is asserted at the Decide level and recorded as such.
		denied := admission.Decide(admission.Gate{}, x17OwnedTarget, admission.Failure{Stage: admission.StagePrivacy})
		require.Equal(t, admission.OutcomeDeny, denied.Outcome)
		require.Equal(t, admission.ReasonPrivacyDenied, denied.Reason)
		require.Empty(t, denied.Handle)
		require.Equal(t, admission.OutcomeDeny,
			admission.Decide(admission.Gate{Enabled: true, Owned: true}, x17OwnedTarget, admission.Failure{Stage: admission.StagePrivacy}).Outcome)
		require.Equal(t, admission.OutcomePassThrough,
			admission.Decide(admission.Gate{Enabled: true, Owned: true}, x17OwnedTarget, admission.Failure{Stage: admission.StagePolicy}).Outcome,
			"an unanswerable check is never promoted to a denial")
	})

	t.Run("quality_is_recorded_against_unmodified_output_and_replacement_stays_off", func(t *testing.T) {
		// Nothing observed is inconclusive, never a pass.
		var none admission.Comparison
		require.Equal(t, admission.VerdictInconclusive, none.Verdict())
		require.False(t, none.Enables())

		layers := func(retained int) map[admission.Layer]admission.LayerResult {
			return map[admission.Layer]admission.LayerResult{
				admission.LayerTaskCompletion: admission.Predeclare(x17Margin).Observe(x17Tasks, x17Tasks),
				admission.LayerConstraints:    admission.Predeclare(x17Margin).Observe(x17Tasks, x17Tasks),
				admission.LayerRecoverability: admission.Predeclare(x17Margin).Observe(retained, x17Tasks),
			}
		}
		retained := admission.Comparison{Layers: layers(x17Tasks)}
		require.Equal(t, admission.VerdictRetained, retained.Verdict())
		require.True(t, retained.Enables(), "the quality gate did not block")

		// One layer regressing is a regression whatever the others say; a layer nobody measured is
		// inconclusive; a result assembled without a predeclared margin is inconclusive at any count.
		regressed := admission.Comparison{Layers: layers(x17Tasks / 2)}
		require.Equal(t, admission.VerdictRegressed, regressed.Verdict())
		require.False(t, regressed.Enables())
		partial := admission.Comparison{Layers: layers(x17Tasks)}
		delete(partial.Layers, admission.LayerRecoverability)
		require.Equal(t, admission.VerdictInconclusive, partial.Verdict())
		require.Equal(t, admission.VerdictInconclusive, admission.LayerResult{Observed: x17Tasks, Retained: x17Tasks}.Verdict())

		// Cost and latency are recorded separately: the comparison has no field to carry them, so
		// no arithmetic in the verdict can reach one.
		for _, name := range append(x17FieldNames(admission.Comparison{}), x17FieldNames(admission.LayerResult{})...) {
			for _, forbidden := range []string{"cost", "latency", "token", "price"} {
				require.NotContains(t, name, forbidden, "a %s field would let a cost-only result enable admission", forbidden)
			}
		}

		// A retained quality verdict enables nothing by itself: the config gate still refuses the
		// switch, the register still says disabled, and a pipeline over the real config still
		// records disabled.
		cfg, _ := x17OptInRefused(t)
		require.False(t, cfg.Runtime.Migration.Replacement.NewResult, "replacement stays off until the target and regression gates pass")
		cap, ok := contract.DefaultCapabilityRegister().Get(contract.CapNewResultReplacement)
		require.True(t, ok)
		require.False(t, cap.Enabled)
		rec := x17Admit(t, x17GateFromConfig(cfg), r.Ports(true), x17Owned(x17Payload(t, x17Failure(8, "sig-Q", "quality"))))
		x17RequireRefused(t, rec, admission.ReasonDisabled)
	})

	r.P.AssertAppendOnly(t)
}
