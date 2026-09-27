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
	// test/replay/l3policy (SP-12) is the replay driver's qompack-l3 policy: a library over
	// scheduler/eval/config/core/paths, declared here because classify() knows test/ paths only
	// through this table. TestPolicy_DoesNotImportDaemon keeps it off the composition roots.
	"test/replay/l3policy": true,
	// test/canary (SP-19 M0-03) is the host-contract canary suite: it builds and drives the real
	// binary, reads the committed plugin manifest, speaks JSON-RPC to `qompack mcp` and reports
	// against internal/contract's capability register. Spanning cli/mcp/pluginmanifest/contract at
	// once is what no internal package's allow-set permits and what a composition root exists to
	// hold — the same reason test/e2e is one.
	"test/canary": true,
	// test/docs (SP-18) is the documentation harness: it reads README.md, docs/**/*.md and the
	// generated command and tool pages as FILES, imports nothing from internal/, and nothing
	// imports it — the same half of the rule every test/ package above is here for.
	"test/docs": true,
	// test/platform (SP-17 Task 2) is the deployment-environment matrix: it assembles a real
	// plugin bundle through `devtool bundle`, drives that bundle's binary across awkward path
	// shapes, shell launcher forms and managed permission restrictions, and speaks admin IPC to
	// the daemon it starts. It therefore reaches daemon, ipc, paths, hookio, core and testutil
	// directly and cli through the binary it spawns, which no internal allow-set permits — the
	// same reason test/e2e and test/canary are roots. Nothing imports it back.
	"test/platform": true,
	// test/security (SP-17 Task 3) is the trust-and-privacy matrix: it assembles a real plugin
	// bundle through `devtool bundle`, drives that bundle's binary and its `qompack mcp` server
	// against denied and escaping addresses, sweeps every durable surface for planted credentials,
	// and seeds malformed objects straight into the store. It therefore reaches store, mcp, eval,
	// config, sketch, daemon, ipc, hookio, paths, core and testutil directly and cli through the
	// binary it spawns, which no internal allow-set permits — the same reason test/e2e, test/canary
	// and test/platform are roots. Nothing imports it back.
	"test/security": true,
	// test/fault (SP-17 Task 4) is the fault-and-recovery matrix: it assembles a real plugin bundle
	// through `devtool bundle`, cuts a real detached daemon and the files it wrote at every
	// publication boundary, drives the lifecycle and child-failure matrices through that bundle's
	// binary and its `qompack mcp` server, and then walks `.qompack/` resolving every reference the
	// product left behind. It therefore reaches store, checkpoint, daemon, ipc, paths, config,
	// logging, core and testutil directly and cli through the binary it spawns, which no internal
	// allow-set permits — the same reason test/e2e, test/canary, test/platform and test/security are
	// roots. Nothing imports it back.
	"test/fault": true,
	// test/release (SP-17 Task 7) is the independent-switch matrix: it assembles a real plugin
	// bundle through `devtool bundle`, drives that bundle's binary with one configuration switch
	// flipped at a time, and speaks admin IPC to the daemon it started so the NEXT hook starts one
	// that actually read the switch. It therefore reaches daemon, ipc, paths, hookio, core and
	// testutil directly and cli through the binary it spawns, which no internal allow-set permits —
	// the same reason test/e2e, test/canary, test/platform, test/security and test/fault are roots.
	// Nothing imports it back.
	"test/release": true,
}

// allow is the §3.2 layer-mapping table, transcribed verbatim. Every non-foundation package
// additionally gets foundation appended at check time (see effectiveAllow). A package that exists
// on disk but is absent from both allow and compositionRoots is an error: new packages must be
// declared here, which forces an architecture amendment.
var allow = map[string][]string{
	"core":  {},
	"paths": {"core"},
	// config -> paths is owner decision D22 (2026-09-26): the config loaders name the user-global
	// root through paths.Global and read config.json with delete sharing. paths imports only core,
	// so the edge closes no cycle.
	"config": {"core", "paths"},

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

	// mcp gains hostperm at the V6 close-out (C1.9, V6-HOST-1): every retrieval form re-checks a
	// stored path against the host's current Read deny and ask rules before content is served.
	"mcp": {"store", "negknow", "checkpoint", "hostperm"},

	"contract": {"hookio", "store"},
	"ipc":      {"hookio", "contract"},
	"observer": {"hookio", "store", "chunk", "canon", "sketch", "dag", "grammar", "negknow", "tokens"},

	"pluginmanifest": {},

	// state (SP-20 M2-01) is foundation-only: internal/state's only non-stdlib import is core.
	// It has no consumers wired yet — that is a later task's wiring change, not an import-rule one.
	"state": {},

	// admission (SP-21 M4) is on disk as of SP-21 commit 1. The reservation this entry made came
	// first, because SP-21 makes the architecture amendment a precondition of any authoring, and
	// §3.2 is where that reservation is made — this is its transcription.
	//
	// Foundation-only is a design commitment, not a placeholder: the pipeline reaches SP-20
	// capture/publication and SP-13 handle resolution through ports it declares itself, satisfied
	// at a composition root. If a future slice needs a real internal/ import, that is another
	// amendment to §3.2 — which is exactly the control this table exists to impose.
	"admission": {},

	// hostperm (V6 close-out C1.9, owned by SP-13) is foundation-only: it reads Claude Code's
	// settings files and evaluates their Read deny/ask rules for one path, which needs paths and
	// core and nothing else in internal/. mcp is its only consumer.
	"hostperm": {},
}
