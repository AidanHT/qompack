package mathx

// Sign returns -1, 0 or 1 according to the sign of x.
func Sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	}
	return 0
}
