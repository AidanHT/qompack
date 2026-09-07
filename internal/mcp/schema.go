package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// This file is the in-repo JSON Schema subset the eight §8.7 tools are described with, and the
// validator that gates both a live `tools/call` and the conformance suite.
//
// It is deliberately NOT a general JSON Schema implementation. It covers exactly what the eight
// published schemas use — type, properties, required, additionalProperties:false, enum, minimum,
// maximum, default, description, items — and rejects anything else at Compile time, so a schema
// that quietly grew a keyword this validator ignores fails at registration rather than passing
// every argument through unchecked. §7.1 is why there is no dependency here: the plugin talks to
// nobody, and that includes a schema library's own network-fetching $ref resolver.

// The schema types this subset understands. Anything else is a Compile error.
const (
	typeObject  = "object"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
	typeArray   = "array"
)

// The Violation messages. They are fixed strings, not formatted prose, because tests assert them
// exactly and because a model reading `invalid arguments for expand: /span: expected string`
// needs the same words every time to learn the shape.
const (
	msgRequiredMissing = "required property missing"
	msgUnknownProperty = "unknown property"
	msgNotInEnum       = "value not in enum"
	msgBelowMinimum    = "below minimum"
	msgAboveMaximum    = "above maximum"
)

// Violation is one schema failure: where it happened, and what was wrong.
type Violation struct {
	// Pointer is an RFC-6901 JSON Pointer into the document, e.g. "/k".
	Pointer string
	// Message is one of the fixed strings above, or "expected <type>".
	Message string
}

// String renders a Violation as `<pointer>: <message>`, the form the tool-error text uses.
func (v Violation) String() string { return v.Pointer + ": " + v.Message }

// Schema is one compiled node of the subset above.
type Schema struct {
	typ string

	// properties and order are an object node's children and their declaration order. Order is
	// kept so ApplyDefaults and Validate produce a deterministic sequence rather than Go's
	// randomized map iteration.
	properties map[string]*Schema
	order      []string

	required   []string
	additional bool

	enum []string

	minimum, maximum *float64

	def    json.RawMessage
	hasDef bool

	items *Schema
}

// rawNode is one schema object as it appears in the published JSON, before compilation.
type rawNode struct {
	Type                 string                     `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties *bool                      `json:"additionalProperties"`
	Enum                 []string                   `json:"enum"`
	Minimum              *float64                   `json:"minimum"`
	Maximum              *float64                   `json:"maximum"`
	Default              json.RawMessage            `json:"default"`
	Description          string                     `json:"description"`
	Items                json.RawMessage            `json:"items"`
}

// knownKeywords is every keyword this subset accepts. A schema carrying anything else is refused
// at Compile: an ignored keyword is a validation hole that looks like a passing test.
var knownKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true,
	"enum": true, "minimum": true, "maximum": true, "default": true,
	"description": true, "items": true,
}

// Compile parses raw into a Schema, reporting the first keyword or type it does not support.
func Compile(raw json.RawMessage) (*Schema, error) {
	return compileAt(raw, "")
}

// compileAt is Compile with the JSON Pointer of the node being compiled, so an error names where
// in a nested schema the problem is.
func compileAt(raw json.RawMessage, ptr string) (*Schema, error) {
	where := ptr
	if where == "" {
		where = "(root)"
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("mcp: schema %s: %w", where, err)
	}
	for k := range keys {
		if !knownKeywords[k] {
			return nil, fmt.Errorf("mcp: schema %s: unsupported keyword %q", where, k)
		}
	}

	var n rawNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("mcp: schema %s: %w", where, err)
	}
	switch n.Type {
	case typeObject, typeString, typeInteger, typeBoolean, typeArray:
	case "":
		return nil, fmt.Errorf("mcp: schema %s: missing type", where)
	default:
		return nil, fmt.Errorf("mcp: schema %s: unsupported type %q", where, n.Type)
	}

	s := &Schema{
		typ:        n.Type,
		required:   n.Required,
		enum:       n.Enum,
		minimum:    n.Minimum,
		maximum:    n.Maximum,
		additional: n.AdditionalProperties == nil || *n.AdditionalProperties,
	}
	if len(n.Default) > 0 {
		s.def, s.hasDef = n.Default, true
	}

	if n.Type == typeObject && len(n.Properties) > 0 {
		s.properties = make(map[string]*Schema, len(n.Properties))
		// Declaration order comes off the raw bytes, not off the decoded map: encoding/json
		// discards object order, and a randomized order would make ApplyDefaults' output and
		// Validate's violation list differ run to run.
		s.order = objectKeyOrder(keys["properties"])
		for _, name := range s.order {
			child, err := compileAt(n.Properties[name], ptr+"/"+escapePointer(name))
			if err != nil {
				return nil, err
			}
			s.properties[name] = child
		}
	}

	if n.Type == typeArray {
		if len(n.Items) == 0 {
			return nil, fmt.Errorf("mcp: schema %s: an array must declare items", where)
		}
		item, err := compileAt(n.Items, ptr+"/items")
		if err != nil {
			return nil, err
		}
		s.items = item
	}

	for _, name := range s.required {
		if s.properties == nil || s.properties[name] == nil {
			return nil, fmt.Errorf("mcp: schema %s: required property %q is not declared", where, name)
		}
	}
	return s, nil
}

// objectKeyOrder returns the keys of a JSON object in the order they appear in the bytes.
func objectKeyOrder(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	var out []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return out
		}
		name, ok := k.(string)
		if !ok {
			return out
		}
		out = append(out, name)
		// Skip the value wholesale: decoding into a RawMessage consumes exactly one value
		// however deeply nested it is.
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return out
		}
	}
	return out
}

// escapePointer applies RFC-6901's two escapes so a property name containing "/" or "~" still
// produces an unambiguous pointer.
func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

// Validate reports every way doc fails s, in a deterministic order: missing required properties
// first, in declaration order, then each declared property that is present, then unknown
// properties sorted by name.
//
// It returns nil for a valid document. A doc that is not even JSON is one violation at the root
// rather than an error return, because every caller's next move is identical either way.
func (s *Schema) Validate(doc json.RawMessage) []Violation {
	v, err := decodeAny(doc)
	if err != nil {
		return []Violation{{Pointer: "", Message: "expected " + s.typ}}
	}
	var out []Violation
	s.validate(v, "", &out)
	return out
}

// decodeAny decodes b with UseNumber, so an integer keeps its exact spelling and "5" can be told
// from 5.0 — which is the whole of the "expected integer" check.
func decodeAny(b json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// validate walks one node. A type mismatch stops the descent for that node: reporting "expected
// object" and then every property of the thing that is not an object would bury the real cause.
func (s *Schema) validate(v any, ptr string, out *[]Violation) {
	switch s.typ {
	case typeObject:
		s.validateObject(v, ptr, out)
	case typeString:
		s.validateString(v, ptr, out)
	case typeInteger:
		s.validateInteger(v, ptr, out)
	case typeBoolean:
		if _, ok := v.(bool); !ok {
			*out = append(*out, Violation{Pointer: ptr, Message: "expected " + typeBoolean})
		}
	case typeArray:
		s.validateArray(v, ptr, out)
	}
}

// validateObject checks required, then every declared property present, then unknown properties.
func (s *Schema) validateObject(v any, ptr string, out *[]Violation) {
	m, ok := v.(map[string]any)
	if !ok {
		*out = append(*out, Violation{Pointer: ptr, Message: "expected " + typeObject})
		return
	}
	for _, name := range s.required {
		if _, present := m[name]; !present {
			*out = append(*out, Violation{Pointer: ptr + "/" + escapePointer(name), Message: msgRequiredMissing})
		}
	}
	for _, name := range s.order {
		child, present := m[name]
		if !present {
			continue
		}
		s.properties[name].validate(child, ptr+"/"+escapePointer(name), out)
	}
	if s.additional {
		return
	}
	unknown := make([]string, 0, len(m))
	for name := range m {
		if s.properties[name] == nil {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		*out = append(*out, Violation{Pointer: ptr + "/" + escapePointer(name), Message: msgUnknownProperty})
	}
}

// validateString checks the type and, when one is declared, the enum.
func (s *Schema) validateString(v any, ptr string, out *[]Violation) {
	str, ok := v.(string)
	if !ok {
		*out = append(*out, Violation{Pointer: ptr, Message: "expected " + typeString})
		return
	}
	if len(s.enum) > 0 && !containsString(s.enum, str) {
		*out = append(*out, Violation{Pointer: ptr, Message: msgNotInEnum})
	}
}

// validateInteger checks the type — a fractional number is NOT an integer, which is why decodeAny
// uses json.Number — and then the bounds.
func (s *Schema) validateInteger(v any, ptr string, out *[]Violation) {
	num, ok := v.(json.Number)
	if !ok {
		*out = append(*out, Violation{Pointer: ptr, Message: "expected " + typeInteger})
		return
	}
	i, err := num.Int64()
	if err != nil {
		*out = append(*out, Violation{Pointer: ptr, Message: "expected " + typeInteger})
		return
	}
	f := float64(i)
	if s.minimum != nil && f < *s.minimum {
		*out = append(*out, Violation{Pointer: ptr, Message: msgBelowMinimum})
	}
	if s.maximum != nil && f > *s.maximum {
		*out = append(*out, Violation{Pointer: ptr, Message: msgAboveMaximum})
	}
}

// validateArray checks the type and then every element against the items schema.
func (s *Schema) validateArray(v any, ptr string, out *[]Violation) {
	arr, ok := v.([]any)
	if !ok {
		*out = append(*out, Violation{Pointer: ptr, Message: "expected " + typeArray})
		return
	}
	for i, item := range arr {
		s.items.validate(item, ptr+"/"+strconv.Itoa(i), out)
	}
}

// containsString reports whether set contains s.
func containsString(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

// ApplyDefaults returns doc with every absent top-level property that declares a "default" filled
// in. It is run BEFORE Validate, so a schema default is never itself reported as a violation, and
// so a handler reads `k` rather than `k, ok`.
//
// An empty or absent doc is treated as an empty object — a `tools/call` with no arguments at all
// is the ordinary way `dropped` is invoked.
func (s *Schema) ApplyDefaults(doc json.RawMessage) (json.RawMessage, error) {
	if s.typ != typeObject {
		return doc, nil
	}
	m := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(doc)) > 0 {
		if err := json.Unmarshal(doc, &m); err != nil {
			return doc, fmt.Errorf("mcp: arguments must be a JSON object: %w", err)
		}
	}
	for _, name := range s.order {
		child := s.properties[name]
		if !child.hasDef {
			continue
		}
		if _, present := m[name]; present {
			continue
		}
		m[name] = child.def
	}
	b, err := json.Marshal(m)
	if err != nil {
		return doc, fmt.Errorf("mcp: applying schema defaults: %w", err)
	}
	return b, nil
}
