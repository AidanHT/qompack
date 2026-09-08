package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
)

const (
	captureConfigMaxBytes = 1 << 20
	captureConfigMaxDepth = 128
	captureConfigMaxValue = 64 << 10
	captureConfigMaxRules = 256
)

// LoadForCapture composes the normal five configuration layers without fallback. It is
// read-only and returns no configuration, provenance, path or rejected value on failure.
// Callers must additionally compile the privacy policy before admitting payload bytes.
// Load retains its historical soft diagnostic behavior for other consumers.
//
// Both roots must be explicit absolute paths. Missing files use defaults; existing files
// must be bounded regular leaves. This does not establish trust in ancestor directories.
// Getenv only exposes known names, so unknown or explicitly empty environment variables
// cannot be distinguished from absence through Env's existing lookup contract.
func LoadForCapture(env Env) (Config, Provenance, error) {
	fail := func() (Config, Provenance, error) {
		return Config{}, nil, fmt.Errorf("%w: capture configuration unavailable", core.ErrDegraded)
	}
	if !filepath.IsAbs(env.ProjectRoot) || !filepath.IsAbs(env.HomeDir) {
		return fail()
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
			return fail()
		}
		flags[key] = value
	}
	values := make(map[string]string)
	if env.Getenv != nil {
		for _, path := range globalSchema.leafPaths {
			name := envVarName(path)
			value := env.Getenv(name)
			if !bounded(value) {
				return fail()
			}
			values[name] = value
		}
	}
	merged := toMap(Defaults())
	prov := Provenance{}
	for k := range globalSchema.leaves {
		prov[k] = Source{Origin: OriginDefault, Location: "config.Defaults()"}
	}
	for _, input := range []struct {
		path   string
		origin Origin
	}{
		{filepath.Join(env.HomeDir, ".qompack", "config.json"), OriginUserFile},
		{filepath.Join(env.ProjectRoot, ".qompack", "config.json"), OriginProjectFile},
	} {
		raw, missing, ok := readCaptureConfig(input.path)
		if !ok {
			return fail()
		}
		if missing {
			continue
		}
		stripped, layer, ok := decodeCaptureConfig(raw)
		if !ok {
			return fail()
		}
		var warnings []Warning
		deepMerge(merged, layer, "", prov, input.origin, input.path, locateKeys(stripped), &warnings)
		if len(warnings) != 0 {
			return fail()
		}
	}
	var warnings []Warning
	applyEnv(merged, func(name string) string { return values[name] }, prov, &warnings)
	if _, err := json.Marshal(merged); err != nil {
		return fail()
	}
	applyFlags(merged, flags, prov, &warnings)
	if len(warnings) != 0 {
		return fail()
	}
	// Do not use fromMap: it intentionally falls back on marshal/unmarshal failure, including
	// nonfinite environment floats and numbers that do not fit a target integer field.
	b, err := json.Marshal(merged)
	if err != nil || len(b) > captureConfigMaxBytes {
		return fail()
	}
	var cfg Config
	if json.Unmarshal(b, &cfg) != nil {
		return fail()
	}
	deriveSubmodularEnabled(&cfg)
	if len(cfg.Validate()) != 0 {
		return fail()
	}
	if len(cfg.Runtime.Redact.Patterns) > captureConfigMaxRules {
		return fail()
	}
	for _, pattern := range cfg.Runtime.Redact.Patterns {
		if len(pattern) > captureConfigMaxValue {
			return fail()
		}
	}
	return cfg, prov, nil
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
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
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
