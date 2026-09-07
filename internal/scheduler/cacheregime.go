package scheduler

import (
	"strings"

	"github.com/qompack/qompack/internal/config"
)

// The one-hour prompt-cache regime, from the Claude API reference
// (platform.claude.com/docs/en/build-with-claude/prompt-caching): "1-hour cache write tokens are
// 2 times the base input tokens price". These are documented host pricing figures, not Appendix C
// defaults — Appendix C's scheduler.cache keys describe the five-minute FLOOR — so neither value
// is in §11.6's forbidden sets and neither needs a nomagic allowance.
const (
	// HostOneHourTTLSeconds is the one-hour TTL a Claude subscription requests automatically.
	HostOneHourTTLSeconds = 3600
	// HostOneHourWriteMultiplier is w at the one-hour TTL.
	HostOneHourWriteMultiplier = 2.0
)

// The documented host variables the ladder reads, by their documented names, through the
// caller-supplied getenv (config.Env.Getenv) so tests can inject them.
const (
	envDisablePromptCaching  = "DISABLE_PROMPT_CACHING"
	envForcePromptCaching5M  = "FORCE_PROMPT_CACHING_5M"
	envEnablePromptCaching1H = "ENABLE_PROMPT_CACHING_1H"
)

// The CacheRegime.Source values, one per rung.
const (
	regimeSourceDisabled = "disabled"
	regimeSourceSubagent = "subagent_5m"
	regimeSourceForce5M  = "force_5m"
	regimeSourceEnable1H = "enable_1h"
	regimeSourceUnknown  = "unknown"
)

// modelFamilies are the suffixes DISABLE_PROMPT_CACHING_<FAMILY> is documented for, matched
// case-insensitively against the model id; the first found wins.
var modelFamilies = [...]string{"OPUS", "SONNET", "HAIKU", "FABLE", "MYTHOS"}

// ResolveCacheRegime walks the documented ladder. Highest rung wins:
//
//	a. DISABLE_PROMPT_CACHING or DISABLE_PROMPT_CACHING_<FAMILY> → KNOWN no cache   (0, 0, 1, 1)         "disabled"
//	b. subagent                                                  → KNOWN 5-minute  (cfg TTL, cfg w)     "subagent_5m"
//	c. FORCE_PROMPT_CACHING_5M                                   → KNOWN 5-minute  (cfg TTL, cfg w)     "force_5m"
//	d. ENABLE_PROMPT_CACHING_1H                                  → KNOWN 1-hour    (3600, 3600, r, 2.0) "enable_1h"
//	e. nothing set                                               → UNKNOWN         (cfg TTL, 3600, r, 2.0) "unknown"
//
// A disabled cache outranks everything because a disabled cache is not a short cache. Subagents
// outrank the environment because the Claude Code reference is explicit that they "use the
// five-minute TTL even on a subscription". FORCE_PROMPT_CACHING_5M outranks
// ENABLE_PROMPT_CACHING_1H because the reference says it applies "regardless of authentication"
// and names overriding a managed-settings ENABLE_PROMPT_CACHING_1H as its purpose.
//
// Rung e is the case that matters: it is the default on every machine that has not been
// deliberately configured, and Qompack cannot tell subscription auth from API-key auth (no hook
// input carries it and there is no environment variable for it). So rung e does not guess — it
// reports a RANGE and charges the dearer write price; see UnknownRegime.
//
// The five-minute regime is built from cfg.Cache (Appendix C's floor), never from literals, so a
// re-priced floor is honoured everywhere at once (00-ARCHITECTURE §11.6). A nil getenv reads as an
// empty environment. An empty model, or one naming no known family, skips the per-family rung.
// assumeMaxTTLSeconds is runtime.scheduler.cache.assumeMaxTTLSeconds: it is the upper bound rung e
// reports, and only rung e reads it; a non-positive value falls back to HostOneHourTTLSeconds.
func ResolveCacheRegime(
	getenv func(string) string, cfg config.SchedulerCfg, model string, subagent bool, assumeMaxTTLSeconds int,
) CacheRegime {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if envTruthy(getenv(envDisablePromptCaching)) {
		return disabledRegime()
	}
	if fam := modelFamily(model); fam != "" && envTruthy(getenv(envDisablePromptCaching+"_"+fam)) {
		return disabledRegime()
	}
	if subagent {
		return fiveMinuteRegime(cfg, regimeSourceSubagent)
	}
	if envTruthy(getenv(envForcePromptCaching5M)) {
		return fiveMinuteRegime(cfg, regimeSourceForce5M)
	}
	if envTruthy(getenv(envEnablePromptCaching1H)) {
		return oneHourRegime(cfg)
	}
	return UnknownRegime(cfg, assumeMaxTTLSeconds)
}

// UnknownRegime is the rung the ladder lands on when nothing identifies the session's cache. It
// reports a range — Appendix C's floor for the lower bound, assumeMaxTTLSeconds
// (runtime.scheduler.cache.assumeMaxTTLSeconds; non-positive falls back to the one-hour host
// TTL, and never below the floor) for the upper — and takes the conservative multiplier of the
// two, the larger w. Charging the scheduler the higher write price when the regime is unknown
// biases it toward shallower cuts: a shallower cut than optimal costs reclaim; a deeper cut than
// optimal costs money.
func UnknownRegime(cfg config.SchedulerCfg, assumeMaxTTLSeconds int) CacheRegime {
	ttlMin := cfg.Cache.TTLSeconds
	ttlMax := assumeMaxTTLSeconds
	if ttlMax <= 0 {
		ttlMax = HostOneHourTTLSeconds
	}
	return CacheRegime{
		TTLMinSeconds:   ttlMin,
		TTLMaxSeconds:   max(ttlMax, ttlMin),
		ReadMultiplier:  cfg.Cache.ReadMultiplier,
		WriteMultiplier: max(cfg.Cache.WriteMultiplier, HostOneHourWriteMultiplier),
		Source:          regimeSourceUnknown,
	}
}

// disabledRegime is a session with no prompt cache at all: no read discount, no write premium,
// no TTL and therefore no cold state to exploit.
func disabledRegime() CacheRegime {
	return CacheRegime{
		ReadMultiplier:  1,
		WriteMultiplier: 1,
		Disabled:        true,
		Source:          regimeSourceDisabled,
	}
}

// fiveMinuteRegime is the KNOWN five-minute regime, built entirely from Appendix C's floor.
func fiveMinuteRegime(cfg config.SchedulerCfg, source string) CacheRegime {
	return CacheRegime{
		TTLMinSeconds:   cfg.Cache.TTLSeconds,
		TTLMaxSeconds:   cfg.Cache.TTLSeconds,
		ReadMultiplier:  cfg.Cache.ReadMultiplier,
		WriteMultiplier: cfg.Cache.WriteMultiplier,
		Source:          source,
	}
}

// oneHourRegime is the KNOWN one-hour regime: r is unchanged, w is the documented 2×.
func oneHourRegime(cfg config.SchedulerCfg) CacheRegime {
	return CacheRegime{
		TTLMinSeconds:   HostOneHourTTLSeconds,
		TTLMaxSeconds:   HostOneHourTTLSeconds,
		ReadMultiplier:  cfg.Cache.ReadMultiplier,
		WriteMultiplier: HostOneHourWriteMultiplier,
		Source:          regimeSourceEnable1H,
	}
}

// modelFamily returns the first documented family name found in model, case-insensitively, or
// "" when model is empty or names none of them.
func modelFamily(model string) string {
	if model == "" {
		return ""
	}
	upper := strings.ToUpper(model)
	for _, fam := range modelFamilies {
		if strings.Contains(upper, fam) {
			return fam
		}
	}
	return ""
}

// envTruthy is the host-variable truth rule: the trimmed, lower-cased value is non-empty and not
// one of 0, false, no, off.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}
