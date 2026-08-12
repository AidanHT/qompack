package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Render prints cfg as indented JSON with a trailing "// <origin> <location>" comment on every
// leaf line — the effective configuration annotated with where each value came from. This is
// what `qompack config print --provenance` emits, and it is the first thing
// docs/troubleshooting.md tells a user to run (§11.4).
//
// Keys are sorted at every level for deterministic output. Only leaves carry a comment: p is
// keyed by leaf dotted path (config.Load never records provenance for a section as a whole,
// except the rare cross-field checkpoint.tiers fallback), so a section key such as "scheduler"
// simply has no comment.
func (p Provenance) Render(cfg Config, w io.Writer) error {
	bw := bufio.NewWriter(w)
	if err := p.renderValue(bw, toMap(cfg), "", 0); err != nil {
		return err
	}
	if _, err := bw.WriteString("\n"); err != nil {
		return err
	}
	return bw.Flush()
}

func (p Provenance) renderValue(w *bufio.Writer, v any, path string, indent int) error {
	if obj, ok := v.(map[string]any); ok {
		return p.renderObject(w, obj, path, indent)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("config: Provenance.Render: %s: %w", path, err)
	}
	_, err = w.Write(b)
	return err
}

func (p Provenance) renderObject(w *bufio.Writer, m map[string]any, path string, indent int) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if _, err := w.WriteString("{\n"); err != nil {
		return err
	}
	childIndent := indent + 1
	pad := strings.Repeat("  ", childIndent)
	for i, k := range keys {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if _, err := w.WriteString(pad); err != nil {
			return err
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return err
		}
		if _, err := w.Write(kb); err != nil {
			return err
		}
		if _, err := w.WriteString(": "); err != nil {
			return err
		}
		if err := p.renderValue(w, m[k], childPath, childIndent); err != nil {
			return err
		}
		if i < len(keys)-1 {
			if _, err := w.WriteString(","); err != nil {
				return err
			}
		}
		if src, ok := p[childPath]; ok {
			if _, err := fmt.Fprintf(w, "  // %s %s", src.Origin.String(), src.Location); err != nil {
				return err
			}
		}
		if _, err := w.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err := w.WriteString(strings.Repeat("  ", indent) + "}")
	return err
}
