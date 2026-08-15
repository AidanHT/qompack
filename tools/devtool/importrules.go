package main

// foundation is the five cross-cutting packages of 00-ARCHITECTURE.md §3.2. Every non-foundation
// package may additionally import all of these; the foundation packages themselves get only what
// they are explicitly listed with below (they are not auto-unioned with the rest of foundation).
var foundation = []string{"core", "paths", "config", "logging", "obs"}

// compositionRoots may import anything; nothing may import them. The first five entries are
// §3.2's own list; test/e2e and test/guards are added by SP-01 because they import the whole tree
// and must be subject to the same "nothing may import them" half of the rule.
//
// test/replay is added by SP-02. It is the replay-gate driver, and being a composition root is
// exactly what lets internal/eval stay foundation-only: everything eval needs from a later wave —
// store growth samples, negknow bloom health — is declared as a provider type in eval and supplied
// here, so the dependency lives in the driver rather than in the layer being measured.
var compositionRoots = map[string]bool{
	"daemon":      true,
	"cli":         true,
	"commands":    true,
	"testutil":    true,
	"cmd/qompack": true,
	"test/e2e":    true,
	"test/guards": true,
	"test/replay": true,
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
