package list

import (
	"reflect"
	"testing"
)

func TestParseList(t *testing.T) {
	cases := map[string][]string{
		"a,b":         {"a", "b"},
		" a , b ,, c": {"a", "b", "c"},
		"single":      {"single"},
	}
	for in, want := range cases {
		if got := ParseList(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseList(%q) = %q, want %q", in, got, want)
		}
	}
}
