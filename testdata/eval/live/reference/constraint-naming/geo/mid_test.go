package geo

import "testing"

func TestSpec_Mid(t *testing.T) {
	t.Parallel()
	if got := Mid(Point{}, Point{X: 2, Y: 2}); got != (Point{X: 1, Y: 1}) {
		t.Errorf("Mid = %v", got)
	}
}
