package main

// foundation is the five cross-cutting packages of 00-ARCHITECTURE.md §3.2. Every non-foundation
// package may additionally import all of these; the foundation packages themselves get only what
// they are explicitly listed with below (they are not auto-unioned with the rest of foundation).
var foundation = []string{"core", "paths", "config", "logging", "obs"}

// compositionRoots may import anything; nothing may import them. The first five entries are
// §3.2's own list; test/e2e and test/guards are added by SP-01 because they import the whole tree
// and must be subject to the same "nothing may import them" half of the rule.
//
// Wave 1 adds three more, one per branch, and they are listed together because the merge is where
// the set has to be a union rather than any one branch's view of it. A root that is dropped here
// does not fail loudly: the package simply becomes undeclared, and an undeclared package is an
// error in this checker, so the loss shows up as an unrelated-looking build failure in whichever
// branch's harness was forgotten.
//
//   - test/dedup (SP-04) is the clearest illustration of why the grouping exists: measuring the
//     deduplication ratio requires chunk and canon in ONE package, and §3.2's table deliberately
//     forbids canon from importing chunk. A harness that had to live inside either of them would
//     have forced exactly the dependency the table exists to prevent, so it lives outside
//     internal/ and nothing imports it.
//   - test/bench/hotpath (SP-05 task 7): the hot-path bench harness spawns the real binary and
//     talks to a real daemon child process directly, so — like test/e2e — it needs the whole tree
//     (daemon for StatusSnapshot, ipc for the Client it drives its own admin/status/warm-up
//     traffic through, cli only transitively via the binary it spawns) and nothing may import it
//     back.
//   - test/replay (SP-02) is the replay-gate driver, and being a composition root is exactly what
//     lets internal/eval stay foundation-only: everything eval needs from a later wave — store
//     growth samples, negknow bloom health — is declared as a provider type in eval and supplied
//     here, so the dependency lives in the driver rather than in the layer being measured.
//   - test/integration (V2-VERIFY section 4) exercises cross-component seams that exist only on
//     the merged tree -- chunk + canon + store + dag + eval in one file -- which is precisely
//     what no internal package's allow-set permits and a composition root exists to hold.
var compositionRoots = map[string]bool{
	"daemon":             true,
	"cli":                true,
	"commands":           true,
	"testutil":           true,
	"cmd/qompack":        true,
	"test/e2e":           true,
	"test/guards":        true,
	"test/dedup":         true,
	"test/bench/hotpath": true,
	"test/replay":        true,
	"test/integration":   true,
}

// allow is the §3.2 layer-mapping table, transcribed verbatim. Every non-foundation package
// additionally gets foundation appended at check time (see effectiveAllow). A package that exists
// on disk but is absent from both allow and compositionRoots is an error: new packages must be
// declared here, which forces an architecture amendment.
var allow = map[string][]string{
	"core":   {},
	"paths":  {"core"},
	"config": {"core"},

	"logging": {"core", "paths", "config"},
	"obs":     {"core", "paths", "config"},

	"hookio":  {},
	"sketch":  {},
	"chunk":   {},
	"symbols": {},
	"redact":  {},

	"grammar": {},
	"rules":   {},
	"skills":  {},
	"pins":    {},
	"tokens":  {},

	"eval":      {}, // foundation-only, added implicitly
	"scheduler": {}, // foundation-only, added implicitly

	"canon": {"sketch"},
	"dag":   {},

	"store":   {"chunk", "canon", "sketch", "symbols", "redact", "tokens"},
	"negknow": {"sketch", "store", "dag"},

	"analyzer":   {"store", "dag", "sketch", "scheduler"},
	"checkpoint": {"store", "dag", "negknow", "pins", "grammar", "tokens"},
	"rehydrate":  {"checkpoint", "store", "negknow", "dag", "rules", "skills", "tokens"},

	"mcp": {"store", "negknow", "checkpoint"},

	"contract": {"hookio", "store"},
	"ipc":      {"hookio", "contract"},
	"observer": {"hookio", "store", "chunk", "canon", "sketch", "dag", "grammar", "negknow", "tokens"},

	"pluginmanifest": {},
}
