package money

import (
	"strconv"
	"strings"
)

// FormatPrice renders an amount of euro cents as "12,34 €".
func FormatPrice(cents int64) string {
	var b strings.Builder
	if cents < 0 {
		b.WriteByte('-')
		cents = -cents
	}
	b.WriteString(strconv.FormatInt(cents/100, 10))
	b.WriteByte(',')
	frac := cents % 100
	if frac < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.FormatInt(frac, 10))
	b.WriteString(" €")
	return b.String()
}
