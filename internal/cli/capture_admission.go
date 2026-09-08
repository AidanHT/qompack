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

func readHookCapture(r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 || limit > hookCaptureMaxBytes {
		limit = hookCaptureMaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: hook input unavailable", core.ErrDegraded)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: hook input exceeds capture bound", core.ErrBudget)
	}
	return raw, nil
}

func admitHookCapture(env Env, root string, raw []byte) (hookio.Capture, hookio.Event, config.Config, error) {
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
	policy, err := redact.CapturePolicy(cfg)
	if err != nil {
		return failed(err)
	}
	capture, ev, err := hookio.CaptureHook(raw, int(hookCaptureLimit(int64(cfg.Runtime.HotPath.MaxPayloadBytes))),
		redact.CapturePolicyVersion, policy)
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
	next.PolicyVersion = "redact-json/v1+redact-json/v1"
	return next
}
