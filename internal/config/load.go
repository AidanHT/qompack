package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Load composes five layers, deep-merged per leaf key, lowest precedence first: built-in
// defaults, the user-global file, the project file, QOMPACK_* environment variables, and --set
// flags. It returns a non-nil error only when env.ProjectRoot is empty — every other problem
// (a missing file, an unparseable file, an unknown key, an out-of-range value) is reported
// through the returned []Warning instead, because a hook that dies on bad config takes
// observability with it (§11.3).
//
// Load never logs and never writes a file: §3.2 gives `config` the allow-set {core}, so it
// cannot reach logging.Loud (logging imports config, so the reverse edge would be a cycle) or
// paths.WriteAtomic (config cannot import paths either). Reporting the returned Warnings and
// Violations is the caller's job — see ViolationsFromWarnings.
func Load(env Env) (Config, Provenance, []Warning, error) {
	if env.ProjectRoot == "" {
		return Config{}, nil, nil, fmt.Errorf("config: Env.ProjectRoot must not be empty")
	}

	merged := toMap(Defaults())
	prov := Provenance{}
	for k := range globalSchema.leaves {
		prov[k] = Source{Origin: OriginDefault, Location: "config.Defaults()"}
	}
	var warns []Warning

	apply := func(raw []byte, origin Origin, loc string) {
		stripped := StripJSONC(raw)
		var layer map[string]any
		if err := json.Unmarshal(stripped, &layer); err != nil {
			warns = append(warns, Warning{Key: "", Message: "unparseable config: " + err.Error(), Location: loc})
			return
		}
		lines := locateKeys(stripped)
		deepMerge(merged, layer, "", prov, origin, loc, lines, &warns)
	}

	// §3.2 gives `config` the allow-set {core}: it may not import `paths`, so the two config-file
	// locations are joined inline here with filepath.Join rather than via paths.Global/paths.Of.
	userPath := filepath.Join(env.HomeDir, ".qompack", "config.json")
	projectPath := filepath.Join(env.ProjectRoot, ".qompack", "config.json")
	if b, err := os.ReadFile(userPath); err == nil {
		apply(b, OriginUserFile, userPath)
	}
	if b, err := os.ReadFile(projectPath); err == nil {
		apply(b, OriginProjectFile, projectPath)
	}
	applyEnv(merged, env.Getenv, prov, &warns)
	applyFlags(merged, env.Flags, prov, &warns)

	cfg := fromMap(merged)
	deriveSubmodularEnabled(&cfg)

	// defaults is a private, per-call copy used only to look up fallback values: it may end up
	// aliased into merged by setPath below, but since it is local to this call (never a package
	// var) that aliasing can never leak into another Load call, and every value it can ever
	// supply is itself a default, so even a worst-case self-alias only ever writes a correct
	// value redundantly.
	defaults := toMap(Defaults())
	for _, v := range cfg.Validate() {
		if dv, ok := getPath(defaults, v.Key); ok {
			setPath(merged, v.Key, dv)
		}
		warns = append(warns, Warning{
			Key:      v.Key,
			Location: prov[v.Key].Location,
			Message:  fmt.Sprintf("invalid value, using default: %v not in %v", v.Got, v.Want),
		})
		prov[v.Key] = Source{Origin: OriginDefault, Location: "fallback after violation"}
	}
	cfg = fromMap(merged) // re-derive after fallbacks
	deriveSubmodularEnabled(&cfg)

	return cfg, prov, warns, nil
}

// deriveSubmodularEnabled implements the §5.12 ship-order decision: SubmodularCfg.Enabled is
// never read from a file (json:"-"), it is copied from Runtime.Selection.SubmodularEnabled after
// every fromMap, because fromMap's json.Unmarshal leaves a json:"-" field at its zero value.
func deriveSubmodularEnabled(cfg *Config) {
	cfg.Selection.Submodular.Enabled = cfg.Runtime.Selection.SubmodularEnabled
}

// toMap round-trips c through JSON into a generic tree: map[string]any for objects, []any for
// arrays, float64 for all numbers, plus string/bool/nil. This is the representation Load merges
// layers into, because it is exactly what json.Unmarshal produces for an arbitrary config file
// too — a project file and Defaults() become directly comparable and mergeable.
func toMap(c Config) map[string]any {
	b, err := json.Marshal(c)
	if err != nil {
		panic("config: toMap: Marshal: " + err.Error()) // Config is plain data; cannot fail
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic("config: toMap: Unmarshal: " + err.Error()) // b is our own Marshal output
	}
	return m
}

// fromMap is toMap's inverse. merged is always well-typed by construction — deepMerge,
// applyEnv and applyFlags only ever write values that coerceLeafValue/parseLeafString already
// validated against the target leaf's Go kind — so json.Unmarshal here should never fail. If it
// somehow does (belt-and-suspenders for FuzzConfigLoad), Load falls back to Defaults() rather
// than propagating a decode error its signature has no room for: Load must never panic.
func fromMap(m map[string]any) Config {
	b, err := json.Marshal(m)
	if err != nil {
		return Defaults()
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Defaults()
	}
	return c
}

// deepMerge walks src and, for each leaf present, overwrites dst and records provenance; for
// each key that names a known section it recurses; for anything else it warns "unknown key" and
// drops the value (§11.3 forward compatibility with newer plugin versions writing keys this
// build does not know yet). Arrays are themselves leaves in this schema (TiersCfg.Never and
// friends), so they are always replaced wholesale rather than merged element-wise — the "maps
// merge recursively, arrays replace wholesale" rule falls out of that for free. lines maps a
// dotted path to the 1-based source line it was found on; it is nil for non-file layers.
func deepMerge(dst, src map[string]any, prefix string, prov Provenance, origin Origin, loc string, lines map[string]int, warns *[]Warning) {
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := src[k]
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}

		if li, ok := globalSchema.leaves[path]; ok {
			nv, ok := coerceLeafValue(li, v)
			if !ok {
				*warns = append(*warns, Warning{
					Key:      path,
					Message:  fmt.Sprintf("invalid type for %s: expected %s", path, li.kindName()),
					Location: fileLocation(loc, lines, path),
				})
				continue
			}
			dst[k] = nv
			prov[path] = Source{Origin: origin, Location: fileLocation(loc, lines, path)}
			continue
		}

		if globalSchema.sections[path] {
			sub, ok := v.(map[string]any)
			if !ok {
				*warns = append(*warns, Warning{Key: path, Message: "expected an object", Location: fileLocation(loc, lines, path)})
				continue
			}
			dstSub, ok := dst[k].(map[string]any)
			if !ok {
				dstSub = map[string]any{}
				dst[k] = dstSub
			}
			deepMerge(dstSub, sub, path, prov, origin, loc, lines, warns)
			continue
		}

		*warns = append(*warns, Warning{Key: path, Message: "unknown key", Location: fileLocation(loc, lines, path)})
	}
}

// fileLocation appends ":<line>" to loc when lines has an entry for path, matching the
// "<path>:<line>" format §11.2 specifies for file-layer provenance. Non-file layers (env, flags)
// never pass a non-nil lines map, so their Location is exactly loc.
func fileLocation(loc string, lines map[string]int, path string) string {
	if ln, ok := lines[path]; ok {
		return fmt.Sprintf("%s:%d", loc, ln)
	}
	return loc
}

// coerceLeafValue reports whether v — a value fresh out of json.Unmarshal into `any` — matches
// the Go kind li expects, returning it unchanged (still in generic-JSON form) when it does. This
// is the merge-time type gate that keeps fromMap's later json.Unmarshal into the real Config
// struct from ever failing: an int leaf only ever accepts a whole-number float64, a []string
// leaf only ever accepts an array of strings, and so on. A value of the wrong shape is reported
// as an invalid-type Warning and the merge for that one leaf is skipped, leaving whatever the
// lower-precedence layer already had.
func coerceLeafValue(li leafInfo, v any) (any, bool) {
	switch li.kind {
	case kindBool:
		b, ok := v.(bool)
		return b, ok
	case kindString:
		s, ok := v.(string)
		return s, ok
	case kindFloat:
		f, ok := v.(float64)
		return f, ok
	case kindInt:
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) {
			return nil, false
		}
		return f, true
	case kindStringSlice:
		arr, ok := v.([]any)
		if !ok {
			return nil, false
		}
		for _, e := range arr {
			if _, ok := e.(string); !ok {
				return nil, false
			}
		}
		return arr, true
	case kindFloatPtr:
		if v == nil {
			return nil, true
		}
		f, ok := v.(float64)
		return f, ok
	default:
		return nil, false
	}
}

// kindName renders a leafKind for warning messages.
func (k leafKind) kindName() string {
	switch k {
	case kindBool:
		return "bool"
	case kindInt:
		return "int"
	case kindFloat:
		return "number"
	case kindString:
		return "string"
	case kindStringSlice:
		return "array of strings"
	case kindFloatPtr:
		return "number or null"
	default:
		return "unknown"
	}
}

func (li leafInfo) kindName() string { return li.kind.kindName() }

// applyEnv resolves the QOMPACK_<SEC>__<KEY>__<SUB> environment layer. env.Getenv is a single-
// name lookup, not an enumerator, so this walks every known leaf (not the process environment),
// synthesizes the exact variable name that leaf would use, and asks Getenv for it — an unset
// variable (Getenv returns "") is simply skipped. This makes the layer fully deterministic under
// test without depending on real environment enumeration.
func applyEnv(merged map[string]any, getenv func(string) string, prov Provenance, warns *[]Warning) {
	if getenv == nil {
		return
	}
	for _, path := range globalSchema.leafPaths {
		name := envVarName(path)
		raw := getenv(name)
		if raw == "" {
			continue
		}
		li := globalSchema.leaves[path]
		v, ok := parseLeafString(li, raw)
		if !ok {
			*warns = append(*warns, Warning{
				Key:      path,
				Message:  fmt.Sprintf("invalid env value %s=%q: expected %s", name, raw, li.kindName()),
				Location: name,
			})
			continue
		}
		setPath(merged, path, v)
		prov[path] = Source{Origin: OriginEnv, Location: name}
	}
}

// applyFlags resolves the --set dotted.key=value layer, applied last and so highest precedence.
// Unlike env vars, Flags is fully enumerable and flag keys are matched exactly against the
// dotted schema (the same camelCase spelling a --set author already sees in every example), so
// an unrecognized key is reported as an "unknown key" Warning rather than silently ignored.
func applyFlags(merged map[string]any, flags map[string]string, prov Provenance, warns *[]Warning) {
	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		raw := flags[key]
		li, ok := globalSchema.leaves[key]
		if !ok {
			*warns = append(*warns, Warning{Key: key, Message: "unknown key", Location: "--set"})
			continue
		}
		v, ok := parseLeafString(li, raw)
		if !ok {
			*warns = append(*warns, Warning{
				Key:      key,
				Message:  fmt.Sprintf("invalid --set value %q: expected %s", raw, li.kindName()),
				Location: "--set",
			})
			continue
		}
		setPath(merged, key, v)
		prov[key] = Source{Origin: OriginFlag, Location: "--set"}
	}
}

// envVarName synthesizes the QOMPACK_<SEC>__<KEY>__<SUB> name a dotted path maps to, e.g.
// "scheduler.cache.readMultiplier" -> "QOMPACK_SCHEDULER__CACHE__READMULTIPLIER".
func envVarName(path string) string {
	segs := strings.Split(path, ".")
	for i, s := range segs {
		segs[i] = strings.ToUpper(s)
	}
	return "QOMPACK_" + strings.Join(segs, "__")
}

// parseLeafString parses a raw string (from an env var or a --set flag) into the generic-JSON
// form coerceLeafValue also produces, according to li's kind: bool via strconv.ParseBool,
// numbers via ParseFloat/ParseInt, []string by splitting on ",", and the literal "null" mapping
// a *float64 leaf to nil.
func parseLeafString(li leafInfo, raw string) (any, bool) {
	switch li.kind {
	case kindBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, false
		}
		return b, true
	case kindString:
		return raw, true
	case kindFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, false
		}
		return f, true
	case kindInt:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		return float64(n), true
	case kindStringSlice:
		parts := strings.Split(raw, ",")
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = p
		}
		return out, true
	case kindFloatPtr:
		if raw == "null" {
			return nil, true
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, false
		}
		return f, true
	default:
		return nil, false
	}
}

// getPath reads the value at a dotted path out of a generic JSON tree.
func getPath(m map[string]any, path string) (any, bool) {
	segs := strings.Split(path, ".")
	var cur any = m
	for _, s := range segs {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := mm[s]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// setPath writes v at a dotted path into a generic JSON tree, creating intermediate objects as
// needed. Because it treats the final path segment as a plain map write, it works identically
// whether v is a scalar (a normal leaf) or a whole subtree (used by Load's fallback loop to
// reset all of checkpoint.tiers at once when the never/late/first partition itself is invalid).
func setPath(m map[string]any, path string, v any) {
	segs := strings.Split(path, ".")
	cur := m
	for i, s := range segs {
		if i == len(segs)-1 {
			cur[s] = v
			return
		}
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
}

// locateKeys walks stripped (already comment-blanked JSONC, so offsets match the original file)
// with a token-level json.Decoder and returns, for every object key path it finds, the 1-based
// source line that key's value begins on. It is a pure best-effort index: a path absent from the
// result (which should not happen for any path deepMerge actually visits, since both walk the
// same bytes) simply gets no ":<line>" suffix in its Warning/Source Location.
func locateKeys(stripped []byte) map[string]int {
	dec := json.NewDecoder(bytes.NewReader(stripped))
	lines := map[string]int{}
	walkKeyPositions(dec, "", stripped, lines)
	return lines
}

func walkKeyPositions(dec *json.Decoder, prefix string, src []byte, lines map[string]int) {
	tok, err := dec.Token()
	if err != nil {
		return
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return // a scalar value: nothing further to record
	}
	switch delim {
	case '{':
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return
			}
			key, _ := keyTok.(string)
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			lines[path] = lineForOffset(src, dec.InputOffset())
			walkKeyPositions(dec, path, src, lines)
		}
		_, _ = dec.Token() // closing '}'
	case '[':
		for dec.More() {
			walkKeyPositions(dec, prefix, src, lines) // elements do not extend the dotted path
		}
		_, _ = dec.Token() // closing ']'
	}
}

// lineForOffset converts a byte offset into b to a 1-based line number by counting newlines
// before it. b is always the comment-blanked bytes StripJSONC produced, so this is exact for
// both the stripped copy and (because StripJSONC preserves every original byte's offset) the
// file the caller reads Location against.
func lineForOffset(b []byte, offset int64) int {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(b)) {
		offset = int64(len(b))
	}
	line := 1
	for _, c := range b[:offset] {
		if c == '\n' {
			line++
		}
	}
	return line
}
