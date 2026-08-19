// Package integration holds the V2 checkpoint's cross-component tests: seams
// that exist only on the merged tree, exercised with every dependency real
// (plans/V2-VERIFY-primitives-store-dag-and-baseline.md section 4). It is a
// composition root in tools/devtool/importrules.go -- it imports across the
// layer allow-sets deliberately, and nothing may import it.
package integration
