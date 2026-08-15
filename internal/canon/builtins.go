package canon

// The generic canonicalizer names, in the 00-ARCHITECTURE.md §5.6 registration order.
// The seven per-tool names join them in the commit that adds the per-tool rules. They are
// named constants rather than string literals scattered across the package because Result.Applied
// carries them into SP-06's store records and SP-08's observability output: they are a wire
// format, and a typo in one would be invisible until a dashboard came up empty.
const (
	nameCRLF       = "crlf"
	nameANSI       = "ansi"
	nameTimestamps = "timestamps"
	nameDurations  = "durations"
	namePIDs       = "pids"
	nameAddresses  = "addresses"
	nameTmpPaths   = "tmpPaths"
)

// builtinNames is the §5.6 registration order as data, so Default's table and the tests that pin
// it read from one list rather than two that can drift apart.
var builtinNames = []string{
	nameCRLF, nameANSI, nameTimestamps, nameDurations, namePIDs, nameAddresses, nameTmpPaths,
}

// builtins constructs the built-in canonicalizers, in builtinNames order. Each is stateless, so a
// fresh set per Default call costs one small allocation apiece and removes any question of two
// registries sharing mutable state.
func builtins() []Canonicalizer {
	return []Canonicalizer{
		newCRLF(), newANSI(), newTimestamps(), newDurations(), newPIDs(), newAddresses(),
		newTmpPaths(),
	}
}
