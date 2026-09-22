package money_test

import (
	"testing"

	"example.com/money/money"
)

func TestHiddenEvalFormatPrice(t *testing.T) {
	cases := map[int64]string{1234: "12,34 €", -5: "-0,05 €", 0: "0,00 €", 100: "1,00 €", 7: "0,07 €"}
	for in, want := range cases {
		if got := money.FormatPrice(in); got != want {
			t.Errorf("FormatPrice(%d) = %q, want %q", in, got, want)
		}
	}
}
