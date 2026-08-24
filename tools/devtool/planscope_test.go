package main

import "testing"

func TestOwnersKeyOf_ResolvesEverySpellingThePlansUse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"./internal/eval", "eval"},
		{"./internal/eval/", "eval"},
		{"./internal/eval/...", "eval"},
		{"./internal/eval/evaltest", "eval"}, // a conformance subpackage answers to its parent's row
		{"./cmd/qompack", "cmd/qompack"},
		{"./test/guards", "test"}, // outside internal/, so no OWNERS.tsv row will ever match
		{"./...", ""},             // the module root names no single package
		{"./internal/...", ""},    // nor does the internal-wide wildcard
	}
	for _, c := range cases {
		if got := ownersKeyOf(c.in); got != c.want {
			t.Errorf("ownersKeyOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPkgTestSetIsSettled_LandedOwnerAndNobodyStillWriting(t *testing.T) {
	// The rule that widens the zero-match check past document scope. eval belongs to a landed
	// subplan and nobody is adding to it, so a wave-6 row naming a test that is not there is dead
	// prose. store also belongs to a landed subplan, but SP-16 is still writing segment blooms into
	// it, so a wave-5 row naming one of those is a correct forward reference and must not fail.
	ownerOf := map[string]string{"eval": "SP-02", "store": "SP-06", "mcp": "SP-13"}
	unsettled := map[string]bool{"store": true}

	cases := []struct {
		pkg  string
		want bool
		why  string
	}{
		{"./internal/eval/", true, "owner landed, nobody still writing"},
		{"./internal/eval/...", true, "the wildcard resolves to the same settled package"},
		{"./internal/store/", false, "SP-16 is still writing into it"},
		{"./internal/mcp/", false, "SP-13 has not landed"},
		{"./test/guards", false, "no OWNERS.tsv row, so nothing guarantees its test set is closed"},
		{"./...", false, "spans packages nobody has written"},
	}
	for _, c := range cases {
		if got := pkgTestSetIsSettled(ownerOf, unsettled, c.pkg); got != c.want {
			t.Errorf("pkgTestSetIsSettled(%q) = %v, want %v — %s", c.pkg, got, c.want, c.why)
		}
	}
}

func TestDeliberateNoMatch_CoversBothSpellingsOfRunNothing(t *testing.T) {
	// `-run xxx -fuzz FuzzX` is how eleven plan rows run a fuzz target without its package's tests.
	// Reading the lowercase spelling as a dead pattern would fail every one of them.
	for _, p := range []string{"^$", "XXX", "xxx"} {
		if !deliberateNoMatch[p] {
			t.Errorf("-run %q selects nothing on purpose and must not be reported as unsatisfiable", p)
		}
	}
	if deliberateNoMatch["TestXXX"] {
		t.Error("a real test name containing XXX must still be checked")
	}
}
