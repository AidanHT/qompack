package paths

// The \\?\ long-path prefixes. They are defined once, here, so long_windows.go's real
// implementation and this file's non-Windows identity implementation agree on the exact
// spelling even though only one of the two ever compiles into a given binary.
const (
	// longPrefix is prepended to an absolute, cleaned, non-UNC path.
	longPrefix = `\\?\`
	// longUNCPrefix is prepended, in place of the leading \\, to an absolute, cleaned UNC path
	// (\\server\share\... becomes \\?\UNC\server\share\...).
	longUNCPrefix = `\\?\UNC\`
	// longPathThreshold is the absolute-path length, in characters, at or above which
	// long_windows.go's Long applies a \\?\ prefix. Windows' legacy MAX_PATH is 260; the
	// threshold here is kept a little under that so a short filename appended after Long is
	// called — the randomized suffix os.CreateTemp adds, for instance — never pushes a
	// borderline path over the real limit.
	longPathThreshold = 240
)
