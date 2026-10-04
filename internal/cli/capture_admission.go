package cli

import (
	"bytes"
	"fmt"
	"io"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
)

// Retain the historical four-times transport window, with an independent hard allocation
// cap. Neither stale state nor user configuration may enlarge the capture read beyond it.
const hookCaptureMaxBytes = 4 << 20

func hookCaptureLimit(nominal int64) int64 {
	if nominal <= 0 {
		return 0
	}
	if nominal >= hookCaptureMaxBytes/4 {
		return hookCaptureMaxBytes
	}
	return nominal * 4
}

// hookCaptureRefusalPrefixBytes bounds the evidence a delivery refused by the hard allocation cap
// carries forward. The cap exists to bound memory, so the record it leaves must not smuggle the
// buffer it refused back in: what survives here is small enough to hold the leading host keys an
// operator needs to recognise the delivery (hook_event_name, session_id, cwd, tool_name) and
// nothing like the payload behind them, and it is copied out so the refused read's own backing
// array is released immediately. //nomagic:allow evidence bound for a refused delivery, not a
// budget and not a config default (§11.6) — no configuration reads or writes it.
const hookCaptureRefusalPrefixBytes = 4 << 10

// hookInput is one bounded read of a host payload: the bytes that arrived, and how the read ended.
// Incomplete is an observation of the transport, kept distinct from the truncation this process
// chooses under its own byte budget, so the capture that follows can classify the two separately.
// Oversize is the third ending: the read bound itself refused the delivery, so Raw holds only a
// bounded prefix of it and Observed holds how much of it the read had counted when it stopped.
type hookInput struct {
	Raw        []byte
	Incomplete bool
	Oversize   bool
	// Observed is the number of bytes the read had seen when its bound refused the delivery. It is
	// a floor, not the delivery's size: the read deliberately stops rather than counting to the end
	// of something it has already refused to admit.
	Observed int
}

// readHookCapture reads at most limit bytes, under a hard allocation cap no configuration can
// raise. A payload past that cap is refused here, before any configuration is loaded or any
// environment is scanned, so the resource bound never depends on policy being available; the
// policy-derived bound inside hookio.CaptureHook is the one that can retain a bounded prefix.
// A read that ends early returns what did arrive, marked incomplete, rather than nothing at all.
//
// A refusal is not an absence either. The delivery stays refused — nothing downstream admits it,
// no Event is derived from it, and the bytes past the bound are never read — but the refusal
// itself travels: the size the read had counted when it stopped, and a prefix bounded far below
// the cap. Returning nothing here is what made a delivery over the hard cap vanish with no durable
// trace at all, which satisfies invariant 1's "missing originals remain explicitly unavailable" at
// the configured budget while violating it one bound higher up. Whether that record can actually
// be written is the caller's question, not this one's — see hookRefusalIsRecordable.
func readHookCapture(r io.Reader, limit int64) (hookInput, error) {
	if limit < 0 || limit > hookCaptureMaxBytes {
		limit = hookCaptureMaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return hookInput{Raw: raw, Incomplete: true}, fmt.Errorf("%w: hook input unavailable", core.ErrDegraded)
	}
	if int64(len(raw)) > limit {
		return hookInput{
			Raw:      bytes.Clone(raw[:min(len(raw), hookCaptureRefusalPrefixBytes)]),
			Oversize: true,
			Observed: len(raw),
		}, fmt.Errorf("%w: hook input exceeds capture bound", core.ErrBudget)
	}
	return hookInput{Raw: raw}, nil
}

// hookRefusalIsRecordable reports whether a refused read may be carried forward as a record.
//
// A read that delivered nothing at all classified nothing, so there is nothing to publish: a
// capture built from it would assert an examination that never happened.
//
// A read the allocation bound refused DID deliver a bounded prefix and a measured floor, which is
// exactly the explicitly-unavailable record invariant 1 requires of a missing original — but only
// where the project already has a .qompack store to hold it. A hook must never conjure state in a
// project that has not opted in, and in one that has not there is genuinely nowhere durable to
// write: the spool, the logs and the daemon address all live under a directory that does not
// exist. There the drop stays the correct outcome, and it is pinned by its own test so that it is
// not later "fixed" into a hook that creates a store out of a payload it refused.
func hookRefusalIsRecordable(root string, in hookInput) bool {
	if len(in.Raw) == 0 {
		return false
	}
	return !in.Oversize || isDir(paths.Of(root).Dot)
}

func admitHookCapture(env Env, root string, in hookInput) (hookio.Capture, hookio.Event, config.Config, error) {
	failed := func(err error) (hookio.Capture, hookio.Event, config.Config, error) {
		return hookio.Capture{}, hookio.Event{}, config.Config{}, err
	}
	if !isDir(root) {
		return failed(fmt.Errorf("%w: capture root unavailable", core.ErrDegraded))
	}
	faultCorruptConfigIfNeeded(root)
	cfg, _, violations, warnings, err := config.LoadForCapture(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	})
	if err != nil {
		return failed(err)
	}
	// runtime.mode off is the operator's instruction that the hook path write nothing at all
	// (troubleshooting §8, Step 3), so it returns before the configuration report below: that report
	// writes state/config-violations.json, creates state/ and tmp/ for it, removes a record an earlier
	// load left, and logs to the day log. Before audit 2's finding #20 it ran first, so a mode-off hook
	// whose file also held an invalid value wrote all of that on every delivery. self-test and doctor
	// still report the condition through their own read-only loads (config.LoadForCapture).
	if cfg.Runtime.Mode == "off" {
		return hookio.Capture{}, hookio.Event{}, cfg, nil
	}
	// A clamped key is a §11.3 violation and a dropped one is a warning, and both must reach an
	// operator where they would look for them. Before finding S-7 and V6 close-out item C1.8 there
	// was nothing to report here, because either one refused the whole delivery instead.
	reportCaptureConfig(root, homeDir(env), violations, warnings)
	// Defence in depth, not the enforcement point. config.Validate now bounds the key from above
	// (HookCaptureHardCapBytes, finding S-2) and the loader clamps it, so a configured value can no
	// longer arrive here above the cap. A Config built some other way still can, and the hard
	// allocation bound answers to nothing but itself.
	if cfg.Runtime.HotPath.MaxPayloadBytes > hookCaptureMaxBytes {
		return failed(fmt.Errorf("%w: capture configuration exceeds supported bound", core.ErrBudget))
	}
	// One compilation, two policies. The fragment policy examines a payload this process cannot
	// admit whole, under the operator's own rules, before any part of it is retained as evidence;
	// compiling those rules a second time would double the most expensive step of admission on the
	// hot path for a case that almost never fires.
	policy, fragment, err := redact.CapturePolicies(cfg)
	if err != nil {
		return failed(err)
	}
	capture, ev, err := hookio.CaptureHook(in.Raw, int(hookCaptureLimit(int64(cfg.Runtime.HotPath.MaxPayloadBytes))),
		redact.CapturePolicyVersion, policy,
		hookio.CaptureFragment{
			Policy: fragment, Incomplete: in.Incomplete,
			// A delivery the read bound already refused arrives here as a prefix. Only the read
			// knows that, so it is the read's own two facts — that the bound fired, and how much
			// it had counted — that let this classify the refusal instead of misreading a cut
			// document as bytes that were merely never valid JSON.
			Oversize: in.Oversize, SourceBytes: in.Observed,
		})
	guarded := scopeGuardCapture(root, capture, ev)
	// Scope the original envelope too: redaction can replace a path, and JSON
	// decoding can collapse duplicate keys. Neither may grant admission.
	if verdict := hookio.CaptureScopeRaw(root, in.Raw).Verdict; verdict.Refuses() && guarded.Recorded() {
		guarded.Bytes = nil
		if capture.Outcome == core.OutcomeOK {
			guarded.Fidelity = core.FidelityUnknown
			guarded.Outcome = core.OutcomeUnavailable
			if verdict == hookio.ScopeOutOfProject {
				guarded.Outcome = core.OutcomeDenied
			}
		}
	}
	if guarded.Outcome != core.OutcomeOK {
		// A refused or degraded delivery carries NO Event: hookio derived none for a degraded one,
		// and a scope refusal must not let the original Event (or the Raw extras composed from it)
		// travel to the spool and reintroduce a path the bytes were just stripped of. Clearing it
		// here is where ipc.WithCapture's composition is made byte-free-safe end to end.
		ev = hookio.Event{}
	}
	return guarded, ev, cfg, err
}

// scopeGuardCapture applies the path-scope trust boundary (V6-AUTH-1, authority-review §5) on the
// hook client, BEFORE the capture can be spooled or sent, so out-of-project file bytes never reach
// the transport spool/WAL — the guarantee the daemon admission gate alone cannot make, because the
// raw line touches the spool before admission runs.
//
// An admitted (OutcomeOK) delivery is judged from its derived Event's structured tool input. Any
// verdict that Refuses reduces it to a byte-free record that REUSES an existing outcome (no new
// schema): a PROVEN escape becomes OutcomeDenied, an UNPROVABLE target becomes OutcomeUnavailable —
// "cannot prove inside" recorded as unavailable, never as a false absence.
//
// A degraded delivery derived no Event; it is judged from its own ALREADY-REDACTED retained bytes
// (what the sidecar would persist — scoping them reintroduces nothing). Only a single, complete,
// unambiguous in-scope object may keep them; a partial prefix cannot prove its nature (field order
// is not a contract, a later path or a duplicate key cannot be ruled out), so its bytes are dropped
// while the degraded classification and SourceBytes — the trace privacy does not require bytes for —
// are preserved.
func scopeGuardCapture(root string, capture hookio.Capture, ev hookio.Event) hookio.Capture {
	if !capture.Recorded() {
		return capture // nothing was classified, so there is nothing to guard
	}
	if capture.Outcome == core.OutcomeOK {
		switch hookio.CaptureScope(root, ev.ToolName, ev.ToolInput).Verdict {
		case hookio.ScopeOutOfProject:
			capture.Bytes, capture.Fidelity, capture.Outcome = nil, core.FidelityUnknown, core.OutcomeDenied
		case hookio.ScopeUnprovable:
			capture.Bytes, capture.Fidelity, capture.Outcome = nil, core.FidelityUnknown, core.OutcomeUnavailable
		}
		return capture
	}
	if len(capture.Bytes) == 0 {
		return capture // already a byte-free evidence record; nothing to strip
	}
	if hookio.CaptureScopeRaw(root, capture.Bytes).Verdict == hookio.ScopeAllow {
		return capture // a complete, unambiguous, in-scope object may be retained as evidence
	}
	capture.Bytes = nil // drop opaque, unprovable-or-outside bytes; keep the classification + SourceBytes
	return capture
}

// Both real policies currently admit only exact or redacted JSON. Preserve earlier admission
// facts when a destination policy leaves its already-redacted input unchanged. This is a
// transient composed registry label, not a persisted policy-config identity or observation.
func composeHookCapture(prior, next hookio.Capture) hookio.Capture {
	next.Redacted = prior.Redacted || next.Redacted
	next.Truncated = prior.Truncated || next.Truncated
	if prior.Fidelity != core.FidelityExact && next.Fidelity == core.FidelityExact {
		next.Fidelity = prior.Fidelity
	}
	if prior.CaptureError != core.CaptureErrorNone && next.CaptureError == core.CaptureErrorNone {
		next.CaptureError = prior.CaptureError
	}
	// The observed delivery size is the host's, measured where the payload entered this process.
	// The second policy sees the first one's output, so its own count describes a later stage.
	if prior.SourceBytes != 0 {
		next.SourceBytes = prior.SourceBytes
	}
	next.PolicyVersion = "redact-json/v1+redact-json/v1"
	return next
}
