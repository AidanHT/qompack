package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// CapturePolicyVersion identifies JSON-aware redaction for captured host deliveries. This
// registry entry is independent of canonicalizer versions used for later derived objects.
const CapturePolicyVersion = "redact-json/v1"

// CapturePolicy compiles the complete configured rule set without logging pattern text. Invalid
// user patterns refuse capture rather than silently retaining bytes under a partial rule set.
// Its returned function is compatible with hookio.CapturePolicy without a package dependency.
func CapturePolicy(cfg config.Config) (func([]byte) (core.CaptureDecision, error), error) {
	r := &rx{}
	if cfg.Runtime.Redact.Enabled {
		custom := compileUserPatterns(cfg.Runtime.Redact.Patterns, nil, nil)
		if len(custom) != len(cfg.Runtime.Redact.Patterns) {
			return nil, fmt.Errorf("%w: capture redaction rules unavailable", core.ErrDegraded)
		}
		r.enabled, r.rules = true, append(builtinRules(), custom...)
	}
	return func(raw []byte) (core.CaptureDecision, error) {
		return captureJSON(raw, r)
	}, nil
}

const (
	captureJSONMaxDepth = 128
	capturePlaceholder  = placeholderOpen + "json_field" + placeholderClose
)

// captureJSON also checks raw spellings. A rule spanning JSON syntax cannot safely be applied
// by replacing a decoded string; refuse that payload instead of dropping the rule. All ordinary
// string matches (including escaped spellings) are handled without invalidating the JSON.
func captureJSON(raw []byte, r *rx) (core.CaptureDecision, error) {
	failure := func() (core.CaptureDecision, error) {
		return core.CaptureDecision{Outcome: core.OutcomeUnavailable, Fidelity: core.FidelityFailure},
			fmt.Errorf("%w: capture JSON cannot be safely redacted", core.ErrContract)
	}
	spans, ok := captureStringSpans(raw)
	if !ok {
		return failure()
	}
	_, matches := r.Redact(raw)
	ends := make([]int, 0, len(spans))
	for end := range spans {
		ends = append(ends, end)
	}
	sort.Ints(ends)
	for _, match := range matches {
		at := sort.SearchInts(ends, match.Offset+match.Len+1)
		if at == len(ends) || match.Offset <= spans[ends[at]] {
			return failure()
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	w := captureWalker{dec: dec, raw: raw, spans: spans, redactor: r}
	value, err := w.value(0)
	if err != nil {
		return failure()
	}
	if _, err := dec.Token(); err != io.EOF {
		return failure()
	}
	d := core.CaptureDecision{Bytes: bytes.Clone(raw), Outcome: core.OutcomeOK, Fidelity: core.FidelityExact}
	if w.changed {
		d.Bytes, err = json.Marshal(value)
		if err != nil {
			return failure()
		}
		d.Fidelity, d.Redacted = core.FidelityRedacted, true
	}
	return d, nil
}

type captureWalker struct {
	dec      *json.Decoder
	raw      []byte
	spans    map[int]int // closing quote + 1 -> opening quote
	redactor *rx
	changed  bool
}

func (w *captureWalker) text(value string) string {
	out, _ := w.redactor.Redact([]byte(value))
	if string(out) == value {
		// A custom rule can match an escaped wire spelling that disappears on JSON decoding.
		// Retain no portion of such a string under an unexamined spelling.
		end := int(w.dec.InputOffset())
		start, ok := w.spans[end]
		if ok {
			if _, hits := w.redactor.Redact(w.raw[start+1 : end-1]); len(hits) != 0 {
				out = []byte(capturePlaceholder)
			}
		}
	}
	if string(out) != value {
		w.changed = true
	}
	return string(out)
}

func (w *captureWalker) sensitiveKey(key string) bool {
	// Reuse the existing assignment/.env key definitions, not a second drifting secret-key list.
	// Only built-ins classify structural keys; custom rules still examine actual input bytes.
	for _, rule := range w.redactor.rules {
		if (rule.name == "assignment_secret" || rule.name == "dotenv_value") &&
			rule.re.MatchString(key+"=qompack_capture_value") {
			return true
		}
	}
	return false
}

func (w *captureWalker) value(depth int) (any, error) {
	if depth > captureJSONMaxDepth {
		return nil, core.ErrBudget
	}
	token, err := w.dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := token.(type) {
	case string:
		return w.text(v), nil
	case json.Delim:
		switch v {
		case '{':
			out, seen := map[string]any{}, map[string]bool{}
			for w.dec.More() {
				token, err := w.dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := token.(string)
				if !ok || seen[key] {
					return nil, core.ErrContract
				}
				seen[key] = true
				retainedKey := w.text(key)
				if _, exists := out[retainedKey]; exists {
					return nil, core.ErrContract
				}
				value, err := w.value(depth + 1)
				if err != nil {
					return nil, err
				}
				if w.sensitiveKey(key) {
					text, isString := value.(string)
					// Preserve a complete placeholder on retries; partial placeholders do not shield
					// other bytes in the value from this structural privacy decision.
					if !isString || placeholderRe.FindString(text) != text || text == "" {
						value, w.changed = capturePlaceholder, true
					}
				}
				out[retainedKey] = value
			}
			_, err := w.dec.Token()
			return out, err
		case '[':
			out := []any{}
			for w.dec.More() {
				value, err := w.value(depth + 1)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
			_, err := w.dec.Token()
			return out, err
		default:
			return nil, core.ErrContract
		}
	default:
		return token, nil // UseNumber retains exact numeric spelling and precision.
	}
}

// captureStringSpans rejects malformed UTF-8 and unpaired JSON surrogate escapes before the
// decoder can replace them with U+FFFD. Valid surrogate pairs and literal replacement characters
// remain admissible. json.Valid supplies syntax/depth validation for the bounded scanner.
func captureStringSpans(raw []byte) (map[int]int, bool) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, false
	}
	spans := map[int]int{}
	for at := 0; at < len(raw); at++ {
		if raw[at] != '"' {
			continue
		}
		start := at
		for at++; at < len(raw) && raw[at] != '"'; at++ {
			if raw[at] != '\\' {
				continue
			}
			at++
			if raw[at] != 'u' {
				continue
			}
			code, _ := strconv.ParseUint(string(raw[at+1:at+5]), 16, 16)
			at += 4
			if code >= 0xdc00 && code <= 0xdfff {
				return nil, false
			}
			if code >= 0xd800 && code <= 0xdbff {
				if at+6 >= len(raw) || raw[at+1] != '\\' || raw[at+2] != 'u' {
					return nil, false
				}
				low, err := strconv.ParseUint(string(raw[at+3:at+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return nil, false
				}
				at += 6
			}
		}
		spans[at+1] = start
	}
	return spans, true
}
