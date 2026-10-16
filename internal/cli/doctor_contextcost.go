package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/qompack/qompack/internal/hostperm"
)

// ── context and cost settings ──────────────────────────────────────────────────────────────────

// The "context" section reports the host's context-size settings as they stand on disk and in this
// process's environment. A plugin cannot set them (a plugin's settings keep only `agent` and
// `subagentStatusLine`, GT/plugins-reference.md:214), so the section exists to show a user what is
// set where. It is informational and keeps three rules:
//
//   - Raw values only. Each layer's keys are reported as written, and every modelSettings entry is
//     listed beside the top-level key, never resolved against it. Doctor computes no effective
//     window: it does not know the session's model, flags or environment.
//   - Status is ok (read) or unknown (could not be read as documented), nothing else, and nothing
//     here changes the exit code.
//   - The server-managed cache and a managed configuration profile are reported present or absent,
//     never decoded.

// The settings keys reported per layer, in report order: the window, each model's window under
// modelSettings, then the two switches (GT/settings-reference.md:2757, 2776-2791, 2964-2976).
const (
	ccKeyWindow  = "autoCompactWindow"
	ccKeyModels  = "modelSettings"
	ccKeyEnabled = "autoCompactEnabled"
	ccKeyIdle    = "idleCompaction"
)

// contextEnvVars are the compaction variables reported as this process sees them
// (GT/env-vars.md:202, 220, 236, 322, 449, 450).
var contextEnvVars = []string{
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE",
	"CLAUDE_CODE_DISABLE_1M_CONTEXT",
	"DISABLE_AUTO_COMPACT",
	"DISABLE_COMPACT",
	"CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS",
}

// ccMaxValueRunes bounds one rendered value, so a hostile settings file cannot flood the report.
const ccMaxValueRunes = 64

// contextCostRows is the section for doctor's own state. A refused home root reads no project
// layer: that root's .claude is the user's, which the user layer already reports.
func (s *doctorState) contextCostRows() []doctorRow {
	getenv := s.env.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	root := s.root
	if s.refused != nil {
		root = ""
	}
	return contextCostRows(hostperm.Options{ProjectRoot: root, Home: homeDir(s.env), Getenv: getenv}, getenv)
}

// contextCostRows reads the settings layers o names and reports them, then the variables getenv
// returns, then what doctor cannot see.
func contextCostRows(o hostperm.Options, getenv func(string) string) []doctorRow {
	layers := hostperm.ReadLayers(o)
	kinds := map[hostperm.LayerKind]int{}
	for _, l := range layers {
		kinds[l.Kind]++
	}
	seen := map[hostperm.LayerKind]int{}
	rows := make([]doctorRow, 0, len(layers)+len(contextEnvVars)+1)
	for _, l := range layers {
		id := "context.layer." + string(l.Kind)
		if kinds[l.Kind] > 1 {
			seen[l.Kind]++
			id += "." + strconv.Itoa(seen[l.Kind])
		}
		row := contextLayerRow(l)
		row.ID = id
		rows = append(rows, row)
	}
	for _, name := range contextEnvVars {
		observed := "unset"
		if v := getenv(name); v != "" {
			observed = "set to " + strconv.Quote(ccClip(v))
		}
		rows = append(rows, doctorRow{
			ID: "context.env." + name, Status: doctorOK, Observed: observed,
			Detail: "as seen by this doctor process; the Claude Code session's own environment can differ",
		})
	}
	rows = append(rows, doctorRow{
		ID: "context.invisible", Status: doctorUnknown, Observed: "not visible to doctor",
		Detail: "the --autocompact flag, the --settings flag and the Claude Code session's own environment " +
			"are invisible to doctor, and it does not know the session's model, so it computes no window",
	})
	return rows
}

// contextLayerRow reports one layer; the caller sets the ID.
func contextLayerRow(l hostperm.Layer) doctorRow {
	where := l.Source
	switch {
	case l.Err != nil:
		return doctorRow{Status: doctorUnknown, Observed: "could not be read", Detail: where + ": " + l.Err.Error()}
	case !l.Present:
		return doctorRow{Status: doctorOK, Observed: "absent", Detail: where}
	case l.Opaque:
		return doctorRow{
			Status: doctorUnknown, Observed: "present; not decoded",
			Detail: where + "; a managed configuration profile this build cannot decode",
		}
	case l.Kind == hostperm.LayerRemote:
		return doctorRow{
			Status: doctorUnknown, Observed: "present; not decoded",
			Detail: where + "; the server-managed settings cache has no documented layout, so doctor " +
				"reads no value from it",
		}
	}
	items, problem := contextLayerItems(l.Data)
	if problem != "" {
		return doctorRow{Status: doctorUnknown, Observed: problem, Detail: where}
	}
	observed := "no context-size keys set"
	if len(items) > 0 {
		observed = strings.Join(items, "; ")
	}
	return doctorRow{Status: doctorOK, Observed: observed, Detail: where + "; values as written"}
}

// contextLayerItems renders the reported keys of one settings document, or names why the document
// cannot be read as documented.
func contextLayerItems(data []byte) (items []string, problem string) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &top); err != nil || top == nil {
		return nil, "does not parse as a JSON object"
	}
	if raw, ok := top[ccKeyWindow]; ok {
		items = append(items, ccKeyWindow+"="+ccRaw(raw))
	}
	if raw, ok := top[ccKeyModels]; ok {
		var models map[string]json.RawMessage
		if err := json.Unmarshal(raw, &models); err != nil || models == nil {
			return nil, ccKeyModels + " is not an object"
		}
		names := make([]string, 0, len(models))
		for name := range models {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			var entry map[string]json.RawMessage
			if err := json.Unmarshal(models[name], &entry); err != nil || entry == nil {
				return nil, ccKeyModels + "." + ccModelKey(name) + " is not an object"
			}
			if w, ok := entry[ccKeyWindow]; ok {
				items = append(items, ccKeyModels+"."+ccModelKey(name)+"."+ccKeyWindow+"="+ccRaw(w))
			}
		}
	}
	for _, k := range []string{ccKeyEnabled, ccKeyIdle} {
		if raw, ok := top[k]; ok {
			items = append(items, k+"="+ccRaw(raw))
		}
	}
	return items, ""
}

// ccRaw renders a JSON value as written, compacted and clipped.
func ccRaw(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return fmt.Sprintf("%q", ccClip(string(raw)))
	}
	return ccClip(b.String())
}

// ccModelKey renders a modelSettings key bare when it reads unambiguously in a dotted path, and
// quoted otherwise.
func ccModelKey(name string) string {
	if name != "" && !strings.ContainsFunc(name, func(r rune) bool {
		return r == '.' || r == ';' || r == '=' || r == '"' || unicode.IsSpace(r) || !unicode.IsPrint(r)
	}) {
		return ccClip(name)
	}
	return strconv.Quote(ccClip(name))
}

// ccClip bounds s to ccMaxValueRunes runes.
func ccClip(s string) string {
	r := []rune(s)
	if len(r) <= ccMaxValueRunes {
		return s
	}
	return string(r[:ccMaxValueRunes]) + "…"
}
