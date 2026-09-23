package geo_test

import (
	"math"
	"testing"

	"example.com/geo/geo"
)

func TestHiddenEvalDist(t *testing.T) {
	if got := geo.Dist(geo.Point{}, geo.Point{X: 3, Y: 4}); math.Abs(got-5) > 1e-9 {
		t.Errorf("Dist((0,0), (3,4)) = %v, want 5", got)
	}
}

func TestHiddenEvalMid(t *testing.T) {
	if got := geo.Mid(geo.Point{}, geo.Point{X: 2, Y: 4}); got != (geo.Point{X: 1, Y: 2}) {
		t.Errorf("Mid((0,0), (2,4)) = %v, want (1,2)", got)
	}
}
