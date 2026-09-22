package mathx

// Lerp interpolates linearly from a to b by t.
func Lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}
