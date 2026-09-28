package list_test

import (
	"reflect"
	"testing"

	"example.com/list/list"
)

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

func TestHiddenEvalJoinList(t *testing.T) {
	if got := list.JoinList([]string{"a", "b"}); got != "a, b" {
		t.Errorf("JoinList(a, b) = %q, want %q", got, "a, b")
	}
	if got := list.JoinList(nil); got != "" {
		t.Errorf("JoinList(nil) = %q, want empty", got)
	}
	for _, in := range [][]string{{"x"}, {"alpha", "beta", "gamma"}} {
		if got := list.ParseList(list.JoinList(in)); !reflect.DeepEqual(got, in) {
			t.Errorf("ParseList(JoinList(%q)) = %q", in, got)
		}
	}
}
