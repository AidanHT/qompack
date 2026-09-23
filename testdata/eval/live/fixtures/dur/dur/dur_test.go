package dur

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := map[string]time.Duration{
		"3h":  3 * time.Hour,
		"10m": 10 * time.Minute,
		"2d":  48 * time.Hour,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}
