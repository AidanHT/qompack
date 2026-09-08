package cli

import (
	"fmt"
	"io"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
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

// hookInput is one bounded read of a host payload: the bytes that arrived, and how the read ended.
// Incomplete is an observation of the transport, kept distinct from the truncation this process
// chooses under its own byte budget, so the capture that follows can classify the two separately.
type hookInput struct {
	Raw        []byte
	Incomplete bool
}

// readHookCapture reads at most limit bytes, under a hard allocation cap no configuration can
// raise. A payload past that cap is refused here, before any configuration is loaded or any
// environment is scanned, so the resource bound never depends on policy being available; the
// policy-derived bound inside hookio.CaptureHook is the one that can retain a bounded prefix.
// A read that ends early returns what did arrive, marked incomplete, rather than nothing at all.
func readHookCapture(r io.Reader, limit int64) (hookInput, error) {
	if limit < 0 || limit > hookCaptureMaxBytes {
		limit = hookCaptureMaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return hookInput{Raw: raw, Incomplete: true}, fmt.Errorf("%w: hook input unavailable", core.ErrDegraded)
	}
	if int64(len(raw)) > limit {
		return hookInput{}, fmt.Errorf("%w: hook input exceeds capture bound", core.ErrBudget)
	}
	return hookInput{Raw: raw}, nil
}

func admitHookCapture(env Env, root string, in hookInput) (hookio.Capture, hookio.Event, config.Config, error) {
	failed := func(err error) (hookio.Capture, hookio.Event, config.Config, error) {
		return hookio.Capture{}, hookio.Event{}, config.Config{}, err
	}
	if !isDir(root) {
		return failed(fmt.Errorf("%w: capture root unavailable", core.ErrDegraded))
	}
	faultCorruptConfigIfNeeded(root)
	cfg, _, err := config.LoadForCapture(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	})
	if err != nil {
		return failed(err)
	}
	if cfg.Runtime.Mode == "off" {
		return hookio.Capture{}, hookio.Event{}, cfg, nil
	}
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
		hookio.CaptureFragment{Policy: fragment, Incomplete: in.Incomplete})
	return capture, ev, cfg, err
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
