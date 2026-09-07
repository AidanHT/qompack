package mcp

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The tests for the in-repo JSON Schema subset: what Compile accepts, what Validate reports, what
// ApplyDefaults fills in, and the frozen bytes of the eight published schemas.
//
// Every rejection case asserts the WHOLE violation list rather than membership in it. The order
// Validate promises — required first in declaration order, then the declared properties that are
// present, then the unknown ones sorted — is what makes `invalid arguments for expand: /span:
// expected string` reproducible, and the run() preamble shows the model violation ZERO and nothing
// else. A test that only checked membership would let that order drift without failing, and the
// symptom would surface as a model that cannot learn the argument shape.

// escapedNameSchema is a hand-written schema whose property names carry the two characters
// RFC 6901 has to escape. None of the eight published schemas contains one, and a pointer escape
// that is never exercised is a pointer escape that is wrong the first time a schema grows a name
// like "a/b" — which is why the coverage is bought with a literal here rather than skipped.
const escapedNameSchema = `{"type":"object","properties":{` +
	`"a/b":{"type":"string"},` +
	`"c~d":{"type":"integer"},` +
	`"e~/f":{"type":"boolean"}` +
	`},"required":["a/b"],"additionalProperties":false}`

// sampleHash is a syntactically complete sha256 address, so an ApplyDefaults case exercises the
// argument shape expand is really called with rather than a stub the schema happens to accept.
const sampleHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// compileToolSchema compiles one published tool schema by name.
func compileToolSchema(t *testing.T, name string) *Schema {
	t.Helper()
	raw, ok := toolSchemas[name]
	require.True(t, ok, "no published schema for tool %q", name)
	s, err := Compile(json.RawMessage(raw))
	require.NoError(t, err, "compiling the %s schema", name)
	return s
}

// compileLiteral compiles a schema literal written by a test.
func compileLiteral(t *testing.T, raw string) *Schema {
	t.Helper()
	s, err := Compile(json.RawMessage(raw))
	require.NoError(t, err, "compiling a test schema literal")
	return s
}

// indentGoldenJSON re-renders raw with the two-space indentation every golden in this repository
// uses, plus the trailing newline a text file ends with.
//
// json.Indent inserts whitespace and changes nothing else, so property order, string escaping and
// every value survive byte for byte. The golden therefore stays reviewable by eye without giving
// up anything it froze; callers pair it with a compactness check on the literal, so the exact wire
// bytes are pinned too.
func indentGoldenJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.Indent(&buf, raw, "", "  "), "indenting %s", raw)
	buf.WriteByte('\n')
	return buf.Bytes()
}

// TestSchemaAcceptsValidArgs asserts a well-formed recall call produces no violations at all.
func TestSchemaAcceptsValidArgs(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolRecall)
	require.Empty(t, s.Validate(json.RawMessage(`{"query":"pool","k":3}`)), "valid recall arguments")
}

// TestSchemaRejectsUnknownProperty asserts additionalProperties:false is enforced, and that the
// pointer names the offending member rather than the object that carries it.
func TestSchemaRejectsUnknownProperty(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolRecall)
	require.Equal(t, []Violation{{Pointer: "/kk", Message: msgUnknownProperty}},
		s.Validate(json.RawMessage(`{"query":"x","kk":1}`)))
}

// TestSchemaRejectsMissingRequired asserts a required property that is absent is reported against
// its own pointer, so the model is told which argument to add rather than that the object is bad.
func TestSchemaRejectsMissingRequired(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolWhy)
	require.Equal(t, []Violation{{Pointer: "/decision_id", Message: msgRequiredMissing}},
		s.Validate(json.RawMessage(`{}`)))
}

// TestSchemaRejectsWrongType asserts "5" is not an integer. It is the case a validator built on
// the default float64 decoding gets wrong, and the reason decodeAny asks for json.Number instead.
func TestSchemaRejectsWrongType(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolRecall)
	require.Equal(t, []Violation{{Pointer: "/k", Message: "expected " + typeInteger}},
		s.Validate(json.RawMessage(`{"query":"x","k":"5"}`)))
	require.Equal(t, []Violation{{Pointer: "/k", Message: "expected " + typeInteger}},
		s.Validate(json.RawMessage(`{"query":"x","k":3.5}`)),
		"a fractional number is not an integer either")
}

// TestSchemaRejectsEnumViolation asserts record_eliminated's scope accepts only the two §8.3
// scopes: an elimination filed under a third one would be invisible to every reader.
func TestSchemaRejectsEnumViolation(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolRecordEliminated)
	require.Equal(t, []Violation{{Pointer: "/scope", Message: msgNotInEnum}},
		s.Validate(json.RawMessage(`{"target":"a","approach":"b","reason":"c","scope":"global"}`)))
}

// TestSchemaRejectsBelowMinimum asserts k=0 is refused. A zero k would make recall answer nothing
// while reporting success, which reads to the model as "the store is empty".
func TestSchemaRejectsBelowMinimum(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolRecall)
	require.Equal(t, []Violation{{Pointer: "/k", Message: msgBelowMinimum}},
		s.Validate(json.RawMessage(`{"query":"x","k":0}`)))
	require.Empty(t, s.Validate(json.RawMessage(`{"query":"x","k":1}`)), "k=1 is inside the bound")
}

// TestSchemaRejectsAboveMaximum asserts k=51 is refused one past the declared ceiling, so the
// bound is inclusive of 50 rather than "about fifty".
func TestSchemaRejectsAboveMaximum(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolRecall)
	require.Equal(t, []Violation{{Pointer: "/k", Message: msgAboveMaximum}},
		s.Validate(json.RawMessage(`{"query":"x","k":51}`)))
	require.Empty(t, s.Validate(json.RawMessage(`{"query":"x","k":50}`)), "k=50 is inside the bound")
}

// TestApplyDefaultsFillsK asserts recall's k default is materialized into the argument object
// before the handler reads it, so the handler reads `k` rather than `k, ok`.
func TestApplyDefaultsFillsK(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolRecall)

	filled, err := s.ApplyDefaults(json.RawMessage(`{"query":"x"}`))
	require.NoError(t, err, "ApplyDefaults")

	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(filled, &members), "the filled document must still be an object")
	require.Contains(t, members, "k", "the default must be PRESENT, not merely zero-valued")

	var args RecallArgs
	require.NoError(t, json.Unmarshal(filled, &args), "decoding the filled document")
	require.Equal(t, 5, args.K)
	require.Empty(t, s.Validate(filled), "a schema default must never violate its own schema")
}

// TestApplyDefaultsFillsFullFalse asserts a FALSE default is filled in too.
//
// It is the case an implementation written against truthiness silently skips, and the consequence
// is not cosmetic: expand's `full` absent and `full:false` are the same answer only for as long as
// something guarantees the key is there.
func TestApplyDefaultsFillsFullFalse(t *testing.T) {
	t.Parallel()
	s := compileToolSchema(t, ToolExpand)

	filled, err := s.ApplyDefaults(json.RawMessage(`{"hash":"` + sampleHash + `"}`))
	require.NoError(t, err, "ApplyDefaults")

	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(filled, &members), "the filled document must still be an object")
	require.Equal(t, "false", string(members["full"]), "full must be filled in as an explicit false")

	var args ExpandArgs
	require.NoError(t, json.Unmarshal(filled, &args), "decoding the filled document")
	require.False(t, args.Full)
	require.Empty(t, s.Validate(filled), "a schema default must never violate its own schema")
}

// TestAllEightSchemasCompile asserts every published schema compiles, and that the schema the
// validator uses is the same one tools/list advertises.
//
// The second half catches a real class of bug: run() validates against toolSchemas while
// tools/list emits ToolDefs' InputSchema, so a schema edited in one place and not the other would
// tell the model one contract and enforce another.
func TestAllEightSchemasCompile(t *testing.T) {
	t.Parallel()
	defs := ToolDefs(toolTestDeps())
	require.Len(t, defs, 8, "the tool set of §8.7 is eight tools")

	for _, def := range defs {
		compiled, err := Compile(def.InputSchema)
		require.NoError(t, err, "the %s schema must compile", def.Name)
		require.NotNil(t, compiled, "the %s schema must compile to a node", def.Name)
		require.Equal(t, toolSchemas[def.Name], string(def.InputSchema),
			"%s is validated against a different schema than tools/list advertises", def.Name)
	}
}

// TestSchemaGoldensStable freezes the eight published schemas.
//
// They are the argument contract a model builds its calls from and the input docs/mcp-tools.md is
// generated from, so a change to any of them is a change to a published interface and has to be
// reviewed as one rather than noticed afterwards.
func TestSchemaGoldensStable(t *testing.T) {
	t.Parallel()
	for _, def := range ToolDefs(toolTestDeps()) {
		var compact bytes.Buffer
		require.NoError(t, json.Compact(&compact, def.InputSchema), "compacting the %s schema", def.Name)
		require.Equal(t, string(def.InputSchema), compact.String(),
			"the %s literal must already be compact, so the indented golden pins its exact bytes", def.Name)

		requireGolden(t, "testdata/golden/mcp/schemas/"+def.Name+".json",
			indentGoldenJSON(t, def.InputSchema))
	}
}

// TestSchemaEscapesPointersPerRFC6901 asserts a property name carrying "/" or "~" reaches the
// model as an escaped pointer, from all three of the paths that build one.
//
// The escape ORDER is what is really under test: "~" has to become "~0" before "/" becomes "~1",
// or the "~1" the second replacement produced would itself be re-escaped and "a/b" would come out
// as "a~01b" — a pointer that resolves to nothing in the document it claims to name.
func TestSchemaEscapesPointersPerRFC6901(t *testing.T) {
	t.Parallel()
	s := compileLiteral(t, escapedNameSchema)

	for _, tc := range []struct {
		name string
		doc  string
		want []Violation
	}{
		{
			name: "required",
			doc:  `{}`,
			want: []Violation{{Pointer: "/a~1b", Message: msgRequiredMissing}},
		},
		{
			name: "declared",
			doc:  `{"a/b":1,"c~d":"x","e~/f":0}`,
			want: []Violation{
				{Pointer: "/a~1b", Message: "expected " + typeString},
				{Pointer: "/c~0d", Message: "expected " + typeInteger},
				{Pointer: "/e~0~1f", Message: "expected " + typeBoolean},
			},
		},
		{
			name: "unknown",
			doc:  `{"a/b":"x","g/h":1}`,
			want: []Violation{{Pointer: "/g~1h", Message: msgUnknownProperty}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, s.Validate(json.RawMessage(tc.doc)))
		})
	}

	require.Equal(t, "/a~1b: "+msgRequiredMissing,
		Violation{Pointer: "/a~1b", Message: msgRequiredMissing}.String(),
		"the rendered form is what the tool-error text carries")
}

// TestPropertySchemaAcceptsGeneratedValidDocs is the rapid property: a document assembled from a
// schema's OWN declarations — its properties, their types, their enums and their bounds — must
// never be reported as violating that schema.
//
// The table cases above each pin one rejection; this pins the complement, which is where a
// validator actually goes wrong. An off-by-one bound, a required check that also fires for a
// present-but-falsy value, or an enum comparison that trimmed its input would pass every rejection
// case above and fail here.
func TestPropertySchemaAcceptsGeneratedValidDocs(t *testing.T) {
	t.Parallel()
	for _, def := range ToolDefs(toolTestDeps()) {
		compiled, err := Compile(def.InputSchema)
		require.NoError(t, err, "compiling the %s schema", def.Name)

		t.Run(def.Name, func(t *testing.T) {
			rapid.Check(t, func(rt *rapid.T) {
				doc := drawSchemaDoc(rt, compiled, "")
				raw, merr := json.Marshal(doc)
				require.NoError(rt, merr, "marshalling a generated %s document", def.Name)
				require.Empty(rt, compiled.Validate(raw),
					"a document drawn from the %s schema must satisfy it: %s", def.Name, raw)
			})
		})
	}
}

// drawSchemaDoc draws one object satisfying s: every required property, plus a drawn subset of the
// optional ones. Nothing outside s.order is ever added, because additionalProperties is false on
// all eight and an undeclared member would make the draw invalid by construction rather than by
// any behaviour of the validator.
func drawSchemaDoc(rt *rapid.T, s *Schema, label string) map[string]any {
	doc := make(map[string]any, len(s.order))
	for _, name := range s.order {
		at := label + "/" + name
		if !containsString(s.required, name) && !rapid.Bool().Draw(rt, at+" present") {
			continue
		}
		doc[name] = drawSchemaValue(rt, s.properties[name], at)
	}
	return doc
}

// drawSchemaValue draws one value inside the node's declared type, enum and bounds.
//
// The integer range falls back to a window either side of zero when a bound is absent rather than
// to the whole int64 domain: an unbounded draw would spend its budget on magnitudes no argument
// ever carries, whereas the integers that matter here are the ones next to a declared bound, which
// rapid reaches on its own.
func drawSchemaValue(rt *rapid.T, s *Schema, label string) any {
	switch s.typ {
	case typeString:
		if len(s.enum) > 0 {
			return rapid.SampledFrom(s.enum).Draw(rt, label)
		}
		return rapid.String().Draw(rt, label)
	case typeInteger:
		lo, hi := int64(-1000), int64(1000)
		if s.minimum != nil {
			lo = int64(*s.minimum)
		}
		if s.maximum != nil {
			hi = int64(*s.maximum)
		}
		return rapid.Int64Range(lo, hi).Draw(rt, label)
	case typeBoolean:
		return rapid.Bool().Draw(rt, label)
	case typeArray:
		n := rapid.IntRange(0, 4).Draw(rt, label+" len")
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, drawSchemaValue(rt, s.items, label+" item"))
		}
		return out
	default:
		return drawSchemaDoc(rt, s, label)
	}
}
