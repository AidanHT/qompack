package config

import (
	"encoding/json"
	"reflect"
	"sort"
)

// jsonSchemaDraft and jsonSchemaTitle anchor the root of every JSONSchema() document.
const (
	jsonSchemaDraft = "https://json-schema.org/draft/2020-12/schema"
	jsonSchemaTitle = "Qompack Configuration"
)

// schemaDefaults is Defaults() in generic-JSON form, used only to populate each leaf's "default"
// schema field. It is read-only for the lifetime of the process (schema.go never writes through
// it), so sharing one package-level copy across calls is safe.
var schemaDefaults = toMap(Defaults())

// JSONSchema renders Config as a draft 2020-12 JSON Schema document, reading the json/doc/rng/
// enum/sec struct tags on every leaf: "default" (from Defaults()), "description" (from doc),
// "enum" (from enum, where present), and "x-qompack-section" (from sec). Key order is
// deterministic — encoding/json sorts map[string]any keys alphabetically, and this function
// additionally sorts every "required" array — so re-running JSONSchema on an unchanged Config
// type always produces byte-identical output, which is what makes it golden-testable
// (TestJSONSchema_Golden).
func (c Config) JSONSchema() []byte {
	node := schemaNodeFor(reflect.TypeOf(Config{}), "")
	node["$schema"] = jsonSchemaDraft
	node["title"] = jsonSchemaTitle
	b, err := json.MarshalIndent(node, "", "  ")
	if err != nil {
		panic("config: JSONSchema: " + err.Error()) // node is built from static reflection over Config
	}
	return b
}

// schemaNodeFor builds the schema node for the Go type at path: an "object" node with a
// "properties"/"required" pair for a struct (recursing into every non-skipped field), or a leaf
// node otherwise.
func schemaNodeFor(t reflect.Type, path string) map[string]any {
	if t.Kind() != reflect.Struct {
		return leafSchemaNode(path)
	}

	props := map[string]any{}
	required := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, skip := jsonLeafName(f)
		if skip {
			continue
		}
		childPath := name
		if path != "" {
			childPath = path + "." + name
		}
		props[name] = schemaNodeFor(f.Type, childPath)
		required = append(required, name)
	}
	sort.Strings(required)

	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// leafSchemaNode builds the schema node for a single leaf at path, using its precomputed
// leafInfo (struct tags) and its value in schemaDefaults.
func leafSchemaNode(path string) map[string]any {
	li, ok := globalSchema.leaves[path]
	if !ok {
		panic("config: JSONSchema: no leaf info for " + path) // every non-struct field is a leaf
	}

	node := map[string]any{
		"description":       li.doc,
		"x-qompack-section": li.sec,
		"default":           defaultLeafJSON(path),
	}
	if li.rng != "" {
		node["x-qompack-range"] = li.rng
	}
	switch li.kind {
	case kindBool:
		node["type"] = "boolean"
	case kindInt:
		node["type"] = "integer"
	case kindFloat:
		node["type"] = "number"
	case kindString:
		node["type"] = "string"
	case kindStringSlice:
		node["type"] = "array"
		node["items"] = map[string]any{"type": "string"}
	case kindFloatPtr:
		node["type"] = []any{"number", "null"}
	}
	if len(li.enum) > 0 {
		vals := make([]any, len(li.enum))
		for i, e := range li.enum {
			vals[i] = e
		}
		node["enum"] = vals
	}
	return node
}

// defaultLeafJSON looks up path's default value out of schemaDefaults.
func defaultLeafJSON(path string) any {
	v, _ := getPath(schemaDefaults, path)
	return v
}
