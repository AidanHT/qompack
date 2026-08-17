package braces

// Every function below hides a brace inside something the scanner must not count: an interpreted
// string, a raw string, a rune literal, a line comment and a block comment.
func inString() string {
	s := "}"
	return s + "{"
}

func inRawString() string {
	s := `}
	still inside the raw string {
`
	return s
}

func inRuneLiteral() bool {
	c := '}'
	return c == '{'
}

func inLineComment() int {
	// }
	return 1
}

func inBlockComment() int {
	/* } and { */
	return 2
}
