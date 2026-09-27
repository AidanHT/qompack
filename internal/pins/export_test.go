package pins

// This file exports one package-internal seam to the external pins_test package, which is where
// every test in this package lives. It is a _test.go file, so nothing here ships in a binary.

import "github.com/qompack/qompack/internal/paths"

// SetBarriersForTest replaces the syncs s appends its log records through, so a test can count them
// or cut an append at one of them (durability_test.go). s must be a store OpenWith returned.
func SetBarriersForTest(s Store, b paths.Barriers) { s.(*pinStore).barriers = b }
