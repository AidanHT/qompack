package modwire_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

// exportedDecls parses this package's non-test sources and returns its exported constant and
// type names. It reads the source rather than trusting Schema(), so a constant or message added
// without a Schema() entry is caught here instead of silently missing from wire.gen.ts.
func exportedDecls(t *testing.T) (consts, types []string) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	pkg, ok := pkgs["modwire"]
	require.True(t, ok, "package modwire not found")
	for _, f := range pkg.Files {
		for _, d := range f.Decls {
			gd, isGen := d.(*ast.GenDecl)
			if !isGen {
				continue
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if gd.Tok != token.CONST {
						continue
					}
					for _, n := range s.Names {
						if n.IsExported() {
							consts = append(consts, n.Name)
						}
					}
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						types = append(types, s.Name.Name)
					}
				}
			}
		}
	}
	sort.Strings(consts)
	sort.Strings(types)
	return consts, types
}

// TestSchema_CoversEveryExportedDeclaration holds Schema() — the generator's only input — to the
// package's actual exported surface.
func TestSchema_CoversEveryExportedDeclaration(t *testing.T) {
	consts, types := exportedDecls(t)
	require.NotEmpty(t, consts)
	require.NotEmpty(t, types)

	s := modwire.Schema()
	covered := map[string]bool{}
	for _, c := range s.Consts {
		require.False(t, covered[c.Go], "constant %s listed twice", c.Go)
		covered[c.Go] = true
	}
	for _, e := range s.Enums {
		for _, m := range e.Members {
			covered[m.Go] = true
		}
	}
	for _, name := range consts {
		require.True(t, covered[name], "exported constant %s is missing from Schema()", name)
	}

	typeCovered := map[string]bool{}
	for _, e := range s.Enums {
		typeCovered[e.Type.Name()] = true
	}
	for _, m := range s.Messages {
		typeCovered[m.Type.Name()] = true
	}
	for _, r := range s.Records {
		typeCovered[r.Name()] = true
	}
	// The error type and the schema's own descriptors are Go-side machinery, not wire shapes.
	for _, goOnly := range []string{"VersionError", "SchemaDef", "Const", "Enum", "Member", "Message"} {
		typeCovered[goOnly] = true
	}
	for _, name := range types {
		require.True(t, typeCovered[name], "exported type %s is missing from Schema()", name)
	}
}

// TestSchema_EnumMembersMatchTheirValues checks each enum lists its members in their declared
// order with the values the Go constants hold.
func TestSchema_EnumMembersMatchTheirValues(t *testing.T) {
	byName := map[string]modwire.Enum{}
	for _, e := range modwire.Schema().Enums {
		byName[e.TS] = e
	}
	values := func(e modwire.Enum) []string {
		out := make([]string, 0, len(e.Members))
		for _, m := range e.Members {
			out = append(out, m.Value)
		}
		return out
	}
	verbs := make([]string, 0)
	for _, v := range modwire.Verbs() {
		verbs = append(verbs, string(v))
	}
	reasons := make([]string, 0)
	for _, r := range modwire.Reasons() {
		reasons = append(reasons, string(r))
	}
	outcomes := make([]string, 0)
	for _, o := range modwire.Outcomes() {
		outcomes = append(outcomes, string(o))
	}
	require.Equal(t, verbs, values(byName["Verb"]))
	require.Equal(t, reasons, values(byName["Reason"]))
	require.Equal(t, outcomes, values(byName["Outcome"]))
}

// TestSchema_MessagesAreStructsWithJSONTags guards the generator's assumptions about its input.
func TestSchema_MessagesAreStructsWithJSONTags(t *testing.T) {
	for _, m := range modwire.Schema().Messages {
		require.Equal(t, reflect.Struct, m.Type.Kind(), m.Type.Name())
		for i := range m.Type.NumField() {
			f := m.Type.Field(i)
			if f.Anonymous {
				continue
			}
			require.NotEmpty(t, f.Tag.Get("json"), "%s.%s needs a json tag", m.Type.Name(), f.Name)
		}
	}
}
