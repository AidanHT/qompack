package list_test

import (
	"reflect"
	"testing"

	"example.com/list/list"
)

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
