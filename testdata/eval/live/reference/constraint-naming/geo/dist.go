package geo

import "math"

// Dist is the Euclidean distance between a and b.
func Dist(a, b Point) float64 {
	return math.Hypot(b.X-a.X, b.Y-a.Y)
}
