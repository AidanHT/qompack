package pinned

import (
	"reflect"
	"testing"

	"example.com/list/list"
)

// This package holds only the pinned test, so the constraint check compiles it against the list
// package alone: a trial that never wrote JoinList still has its ParseList graded.
func TestHiddenEvalParseListPinned(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , ,", nil},
		{"a,b", []string{"a", "b"}},
		{" a , b ,, c ", []string{"a", "b", "c"}},
		{"a;b", []string{"a;b"}},
		{"\ta\t,b", []string{"a", "b"}},
		{"x, x", []string{"x", "x"}},
	}
	for _, c := range cases {
		got := list.ParseList(c.in)
		if !reflect.DeepEqual(got, c.want) || (c.want == nil) != (got == nil) {
			t.Errorf("ParseList(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}
