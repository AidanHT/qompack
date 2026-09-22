package mathx_test

import (
	"math"
	"testing"

	"example.com/mathx/mathx"
)

func TestHiddenEvalClamp(t *testing.T) {
	cases := []struct{ v, lo, hi, want int }{{5, 0, 10, 5}, {-3, 0, 10, 0}, {12, 0, 10, 10}, {0, 0, 0, 0}}
	for _, c := range cases {
		if got := mathx.Clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("Clamp(%d, %d, %d) = %d, want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestHiddenEvalLerp(t *testing.T) {
	cases := []struct{ a, b, t, want float64 }{{0, 10, 0.5, 5}, {2, 4, 0, 2}, {2, 4, 1, 4}, {-1, 1, 0.25, -0.5}}
	for _, c := range cases {
		if got := mathx.Lerp(c.a, c.b, c.t); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Lerp(%v, %v, %v) = %v, want %v", c.a, c.b, c.t, got, c.want)
		}
	}
}

func TestHiddenEvalSign(t *testing.T) {
	cases := []struct{ x, want int }{{-7, -1}, {0, 0}, {3, 1}}
	for _, c := range cases {
		if got := mathx.Sign(c.x); got != c.want {
			t.Errorf("Sign(%d) = %d, want %d", c.x, got, c.want)
		}
	}
}
