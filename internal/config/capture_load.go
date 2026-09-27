package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
)

const (
	captureConfigMaxBytes = 1 << 20
	captureConfigMaxDepth = 128
	captureConfigMaxValue = 64 << 10
	captureConfigMaxRules = 256
)

// capturePolicySection is the configuration subtree the capture privacy policy is compiled from
// (internal/redact's CapturePolicies reads runtime.redact.enabled and runtime.redact.patterns).
const capturePolicySection = "runtime.redact"

// captureSwitchKey is the operator's capture on/off switch: "off" makes every hook admit nothing
// (internal/cli's admitHookCapture), and it is a rung of docs/troubleshooting.md's safe-disable
// ladder.
const captureSwitchKey = "runtime.mode"

// The structural refusal classes. Each is a fixed phrase: a refusal names WHICH rule the input broke
// so an operator can find it (self-test and doctor print it), and never echoes the input itself — no
// value, no key the schema does not know, no path.
const (
	refuseRoots     = "project or home root is not an absolute path"
	refuseBounds    = "an environment or --set value exceeds the capture bounds or is not UTF-8"
	refuseFileLeaf  = "config file is not a bounded regular file"
	refuseFileJSONC = "config file is not a single strict JSONC object"
	refusePolicy    = "a " + capturePolicySection + " setting cannot be applied as written, and the " +
		"capture privacy policy has no per-leaf fallback"
	refuseSwitch = "a " + captureSwitchKey + " setting cannot be applied as written, and the " +
		"capture on/off switch has no per-leaf fallback"
	refuseDecode  = "the effective configuration does not decode"
	refuseInvalid = "the effective configuration is still invalid after fallback"
	refuseRules   = capturePolicySection + ".patterns exceeds the capture rule bounds"
)

// LoadForCapture composes the normal five configuration layers for the hook path. It is read-only.
// On refusal it returns no configuration, provenance, violation or warning, and an error that wraps
// core.ErrDegraded and names only the structural class that refused. Callers must additionally
// compile the privacy policy before admitting payload bytes.
//
// Both roots must be explicit absolute paths. Missing files use defaults; existing files
// must be bounded regular leaves. This does not establish trust in ancestor directories.
// Getenv only exposes known names, so unknown or explicitly empty environment variables
// cannot be distinguished from absence through Env's existing lookup contract.
//
// The contract is config.Load's, per leaf (README "Configuration", docs/config-reference.md): an
// invalid value falls back to its default and is returned as a §11.3 Violation, and an unknown key, a
// leaf of the wrong JSON type, a section that is not an object, or an environment or --set value
// that does not parse for its leaf is dropped and returned as a Warning — the leaf keeps the value
// the layer below gave it. A newer runtime.migration/runtime.phase7 settingsVersion resets that
// block, returned as a Violation so the hook can record it. Whenever this loader does not refuse, the
// configuration returned is exactly the one Load returns for the same Env, which
// TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem pins; the two keys it refuses over instead of
// falling back — runtime.redact and runtime.mode — are in the list below.
//
// That used to be false twice over. Finding S-7 (ada54d1) made an out-of-range VALUE clamp instead of
// refusing; V6 close-out item C1.8 is the rest of it. Every merge Warning still refused the whole
// delivery, so ONE unknown key or ONE mistyped leaf in a project config made every hook admit nothing
// and create no .qompack/ — while `config print` loaded the same file and `self-test` said
// `config.load ok`. Falling back is safe on this path for the reason the capture cap already relies
// on: every value it hands forward is bounded again by internal/cli's own hookCaptureLimit before a
// byte is read.
//
// What stays STRUCTURAL — a refusal of the capture, never a fallback — is everything whose fallback
// would either read unbounded input or capture under a weaker privacy policy than the operator wrote:
//
//   - a root that is not absolute (refuseRoots);
//   - a config file that is not a bounded regular leaf: a directory, a link, over 1 MiB, or swapped
//     between stat and open (refuseFileLeaf);
//   - a config file that is not one strict JSONC object: malformed, not UTF-8, an unterminated
//     comment or string, an unpaired surrogate escape, an empty trailing comma, a duplicate key in
//     either spelling, nesting past 128, a number JSON cannot represent (refuseFileJSONC). A layer
//     that does not parse cannot be applied per leaf, because nobody can say which of its leaves
//     were privacy rules;
//   - an environment or --set key or value past the per-value or aggregate bound, or not UTF-8
//     (refuseBounds);
//   - ANY problem inside runtime.redact: an unknown key there, a leaf of the wrong type, an
//     unparseable environment or --set value for one, or a fallback that would change the block
//     (refusePolicy). Every per-leaf fallback in that subtree captures under a weaker policy than the
//     one configured — an ignored pattern list, or a mistyped `enabled` that leaves a lower layer's
//     `false` in force — so the privacy policy keeps the all-or-nothing admission that
//     internal/redact already applies to a pattern that does not compile;
//   - ANY setting of runtime.mode that cannot be applied as written: a value outside its enum (an
//     operator's "OFF"), a wrong type, an unknown key under it, or a fallback that would change it
//     (refuseSwitch). It is the operator's other control over whether anything is captured, and
//     its fallback — the default "auto", or a lower layer's value — records a project whose
//     operator may have been switching recording off. Refusing does what "off" would have done;
//     a valid value in any layer is applied exactly as before. This narrows S-7's clamp for this
//     one key on this one path; config.Load still clamps it (C1.8 review, finding 2);
//   - an effective configuration that does not decode, that no fallback can make valid, or whose
//     pattern list exceeds the rule bounds (refuseDecode, refuseInvalid, refuseRules).
func LoadForCapture(env Env) (Config, Provenance, []Violation, []Warning, error) {
	fail := func(reason string) (Config, Provenance, []Violation, []Warning, error) {
		return Config{}, nil, nil, nil, fmt.Errorf("%w: capture configuration unavailable: %s", core.ErrDegraded, reason)
	}
	if !filepath.IsAbs(env.ProjectRoot) || !filepath.IsAbs(env.HomeDir) {
		return fail(refuseRoots)
	}
	// Snapshot the enumerable flags and known environment once. Unknown environment names
	// remain outside Env's lookup-only contract, but no known value can bypass these bounds.
	total := 0
	bounded := func(s string) bool {
		total += len(s)
		return len(s) <= captureConfigMaxValue && total <= captureConfigMaxBytes && utf8.ValidString(s)
	}
	flags := make(map[string]string)
	for key, value := range env.Flags {
		if !bounded(key) || !bounded(value) {
			return fail(refuseBounds)
		}
		flags[key] = value
	}
	values := make(map[string]string)
	if env.Getenv != nil {
		for _, path := range globalSchema.leafPaths {
			name := envVarName(path)
			value := env.Getenv(name)
			if !bounded(value) {
				return fail(refuseBounds)
			}
			values[name] = value
		}
	}
	merged := toMap(Defaults())
	prov := Provenance{}
	for k := range globalSchema.leaves {
		prov[k] = Source{Origin: OriginDefault, Location: "config.Defaults()"}
	}
	var warnings []Warning
	for _, input := range []struct {
		path   string
		origin Origin
		layer  string
	}{
		{UserConfigPath(env.HomeDir), OriginUserFile, "user"},
		{ProjectConfigPath(env.ProjectRoot), OriginProjectFile, "project"},
	} {
		raw, missing, ok := readCaptureConfig(input.path)
		if !ok {
			return fail("the " + input.layer + " " + refuseFileLeaf)
		}
		if missing {
			continue
		}
		stripped, layer, ok := decodeCaptureConfig(raw)
		if !ok {
			return fail("the " + input.layer + " " + refuseFileJSONC)
		}
		deepMerge(merged, layer, "", prov, input.origin, input.path, locateKeys(stripped), &warnings)
	}
	applyEnv(merged, func(name string) string { return values[name] }, prov, &warnings)
	applyFlags(merged, flags, prov, &warnings)
	for _, w := range warnings {
		if inCapturePolicy(w.Key) {
			return fail(refusePolicy)
		}
		if inCaptureSwitch(w.Key) {
			return fail(refuseSwitch)
		}
	}
	// defaults is a private, per-call copy used only to look up fallback values; restoreDefault
	// deep-copies whatever it takes from it, so nothing merged holds can alias it.
	defaults := toMap(Defaults())
	// Same reset Load runs: a newer settingsVersion is a known-defaults Warning, not a refusal.
	// Refusing here was the S-7 outage for plan §4's rollback (D8-2): the daemon kept running
	// with the block reset while every hook dropped every capture.
	versionWarns := applyVersionedSections(merged, defaults, prov)
	// Not fromMap: it falls back to Defaults() on a marshal/unmarshal failure, and this loader must
	// refuse rather than substitute. The merge-time type gates (coerceLeafValue, parseLeafString) keep
	// nonfinite floats and integers past the platform int out of merged, so this is the backstop, not
	// the per-leaf rule.
	cfg, ok := decodeCaptureMap(merged)
	if !ok {
		return fail(refuseDecode)
	}
	policy, mode := cfg.Runtime.Redact, cfg.Runtime.Mode

	// §11.3's fallback, run through the same loop Load uses, with a STRICT re-decode: a map that
	// stops decoding under the capture rules is a refusal, never a silent tolerance.
	cfg, clampWarns, ok := clampInvalidLeaves(cfg, merged, defaults, prov, decodeCaptureMap)
	if !ok {
		return fail(refuseDecode)
	}
	if len(cfg.Validate()) != 0 {
		// The fallback restored everything it could name and the result is still invalid, so there
		// is no value this loader can stand behind. That is a refusal, and it is unreachable for
		// any rule confined to one section because Defaults() satisfies every rule.
		return fail(refuseInvalid)
	}
	if cfg.Runtime.Redact.Enabled != policy.Enabled || !slices.Equal(cfg.Runtime.Redact.Patterns, policy.Patterns) {
		// No fallback may rewrite the privacy policy either: not a rule named inside it, and not a
		// section restore widened to one of its ancestors.
		return fail(refusePolicy)
	}
	if cfg.Runtime.Mode != mode {
		// The clamp restores the DEFAULT, "auto", not a lower layer's value, so a project's out-of-enum
		// mode over a user's "off" would record. Neither may the capture switch be rewritten.
		return fail(refuseSwitch)
	}
	if len(cfg.Runtime.Redact.Patterns) > captureConfigMaxRules {
		return fail(refuseRules)
	}
	for _, pattern := range cfg.Runtime.Redact.Patterns {
		if len(pattern) > captureConfigMaxValue {
			return fail(refuseRules)
		}
	}
	violations := ViolationsFromWarnings(clampWarns)
	for _, w := range versionWarns {
		violations = append(violations, Violation{Key: w.Key, Message: w.Message})
	}
	return cfg, prov, violations, warnings, nil
}

// inCapturePolicy reports whether a warning's key names the capture privacy policy or a leaf in it.
// An ANCESTOR does not: a `runtime` that is not an object carries no privacy rule to lose, and the
// merge leaves whatever policy the lower layers set untouched.
func inCapturePolicy(key string) bool {
	return key == capturePolicySection || strings.HasPrefix(key, capturePolicySection+".")
}

// inCaptureSwitch reports whether a warning's key is a setting of runtime.mode: the leaf itself, or
// a key under it that only a --set can spell. As with the policy, an ancestor is not: a `runtime` that
// is not an object sets no mode, and the lower layers' value stays in force.
func inCaptureSwitch(key string) bool {
	return key == captureSwitchKey || strings.HasPrefix(key, captureSwitchKey+".")
}

// decodeCaptureMap re-derives a Config from a merged map under the capture loader's strict rules.
//
// It deliberately does not use fromMap: that one falls back to Defaults() on a marshal/unmarshal
// failure, and this loader must refuse rather than substitute.
func decodeCaptureMap(merged map[string]any) (Config, bool) {
	b, err := json.Marshal(merged)
	if err != nil || len(b) > captureConfigMaxBytes {
		return Config{}, false
	}
	var cfg Config
	if json.Unmarshal(b, &cfg) != nil {
		return Config{}, false
	}
	deriveSubmodularEnabled(&cfg)
	return cfg, true
}

func readCaptureConfig(path string) ([]byte, bool, bool) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, true, true
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > captureConfigMaxBytes {
		return nil, false, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, false
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > captureConfigMaxBytes {
		return nil, false, false
	}
	b, err := io.ReadAll(io.LimitReader(f, captureConfigMaxBytes+1))
	if err != nil || len(b) > captureConfigMaxBytes {
		return nil, false, false
	}
	return b, false, true
}

func decodeCaptureConfig(raw []byte) ([]byte, map[string]any, bool) {
	if !utf8.Valid(raw) || !captureConfigLexicallyValid(raw) {
		return nil, nil, false
	}
	stripped := append([]byte(nil), raw...)
	blankComments(stripped)
	// StripJSONC's legacy grammar also tolerates {,} and [,]. Strict admission requires
	// a value before a trailing comma, then uses the same offset-preserving conversion.
	quoted, escaped := false, false
	previous := byte(0)
	for _, c := range stripped {
		if quoted {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				quoted = false
			}
		} else {
			if c == ',' && (previous == '{' || previous == '[' || previous == ',' || previous == ':') {
				return nil, nil, false
			}
			quoted = c == '"'
		}
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			previous = c
		}
	}
	blankTrailingCommas(stripped)
	d := json.NewDecoder(bytes.NewReader(stripped))
	v, ok := captureConfigValue(d, 0)
	if !ok {
		return nil, nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, nil, false
	}
	m, ok := v.(map[string]any)
	return stripped, m, ok
}

// Validate comment termination and UTF-16 escapes before encoding/json can replace malformed
// strings. The ordinary JSON decoder below still owns syntax, escapes and scalar validation.
func captureConfigLexicallyValid(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			closed := false
			for i++; i < len(raw); i++ {
				if raw[i] == '"' {
					closed = true
					break
				}
				if raw[i] != '\\' {
					continue
				}
				i++
				if i >= len(raw) {
					return false
				}
				if raw[i] != 'u' {
					continue
				}
				if i+4 >= len(raw) {
					return false
				}
				u, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
				if err != nil || u >= 0xdc00 && u <= 0xdfff {
					return false
				}
				i += 4
				if u >= 0xd800 && u <= 0xdbff {
					if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
						return false
					}
					low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
					if err != nil || low < 0xdc00 || low > 0xdfff {
						return false
					}
					i += 6
				}
			}
			if !closed {
				return false
			}
		case '/':
			if i+1 < len(raw) && raw[i+1] == '/' {
				for i += 2; i < len(raw) && raw[i] != '\n' && raw[i] != '\r'; i++ {
				}
			} else if i+1 < len(raw) && raw[i+1] == '*' {
				end := bytes.Index(raw[i+2:], []byte("*/"))
				if end < 0 {
					return false
				}
				i += end + 3
			}
		}
	}
	return true
}

func captureConfigValue(d *json.Decoder, depth int) (any, bool) {
	if depth > captureConfigMaxDepth {
		return nil, false
	}
	tok, err := d.Token()
	if err != nil {
		return nil, false
	}
	switch tok {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			tok, err := d.Token()
			key, ok := tok.(string)
			if err != nil || !ok {
				return nil, false
			}
			if _, duplicate := m[key]; duplicate {
				return nil, false
			}
			v, ok := captureConfigValue(d, depth+1)
			if !ok {
				return nil, false
			}
			m[key] = v
		}
		end, err := d.Token()
		return m, err == nil && end == json.Delim('}')
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, ok := captureConfigValue(d, depth+1)
			if !ok {
				return nil, false
			}
			a = append(a, v)
		}
		end, err := d.Token()
		return a, err == nil && end == json.Delim(']')
	default:
		_, delimiter := tok.(json.Delim)
		return tok, !delimiter
	}
}
