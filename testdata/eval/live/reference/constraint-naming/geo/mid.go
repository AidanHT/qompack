package geo

// Mid is the midpoint of a and b.
func Mid(a, b Point) Point {
	return Point{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
}
