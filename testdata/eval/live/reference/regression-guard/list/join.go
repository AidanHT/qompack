package list

import "strings"

// JoinList joins items with ", ", the inverse of ParseList for items without commas.
func JoinList(items []string) string {
	return strings.Join(items, ", ")
}
