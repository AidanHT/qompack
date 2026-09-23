package dur_test

import (
	"testing"
	"time"

	"example.com/dur/dur"
)

func TestHiddenEvalParse(t *testing.T) {
	ok := map[string]time.Duration{
		"5d": 120 * time.Hour, "1d": 24 * time.Hour, "0m": 0, "7h": 7 * time.Hour, " 15m ": 15 * time.Minute,
	}
	for in, want := range ok {
		got, err := dur.Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"5x", "d", "-1d", "", "1h30m"} {
		if _, err := dur.Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded; want an error", in)
		}
	}
}
