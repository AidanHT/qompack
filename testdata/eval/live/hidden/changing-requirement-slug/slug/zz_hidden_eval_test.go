package slug_test

import (
	"testing"

	"example.com/slug/slug"
)

func TestHiddenEvalSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":  "hello_world",
		"Go  is\tFun!": "go_is_fun",
		"ABC123":       "abc123",
		"a-b":          "ab",
		"x_y":          "x_y",
	}
	for in, want := range cases {
		if got := slug.Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
