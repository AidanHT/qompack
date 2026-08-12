package main

// forbiddenFloats and forbiddenInts are the literal sets of 00-ARCHITECTURE.md §11.6 (D11): a
// float or int literal equal to one of these values, written anywhere outside
// internal/config/defaults.go, a _test.go file, anything under tools/, test/ or testdata/, or an
// explicitly annotated "//nomagic:allow <reason>" line, duplicates a config default and must
// instead be read from config.
//
// forbiddenInts is §11.6's own set plus 8000, exactly as §11.6 instructs: "the set is extended
// with {8000, 12000} when §11.5's rehydrate keys land in SP-01" (12000 was already present in the
// base set; 8000 is the addition SP-01 makes).
var forbiddenFloats = []float64{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}

var forbiddenInts = []int64{20000, 12000, 10000, 8000, 2048, 1024, 4096, 16384, 300, 120, 450}

// containsFloat reports whether v is present in set.
func containsFloat(set []float64, v float64) bool {
	for _, f := range set {
		if f == v {
			return true
		}
	}
	return false
}

// containsInt reports whether v is present in set.
func containsInt(set []int64, v int64) bool {
	for _, i := range set {
		if i == v {
			return true
		}
	}
	return false
}
