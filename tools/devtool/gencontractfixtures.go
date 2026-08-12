package main

import (
	"errors"
	"flag"
)

// taskGenContractFixtures writes/records testdata/golden/contracts/** (implementation spec §16):
// "format" fixtures are regenerated freely, "behaviour" fixtures are recorded once, by the owning
// subplan, via --record <pkg>, and refuse to record while that package's implementation is a stub.
//
// internal/testutil is S7's deliverable within this same subplan (Phase D) and does not exist at
// S1/toolchain authoring time, so this task cannot import it yet without breaking
// `go build ./tools/...` today. It returns a clear, typed error until the integration pass (main
// session, once S7 lands) wires the real generator in here.
func taskGenContractFixtures(args []string) error {
	fs := flag.NewFlagSet("gen-contract-fixtures", flag.ContinueOnError)
	_ = fs.String("record", "", "package name to record a behaviour fixture for (refuses while that package is still a stub)")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	return errors.New("gen-contract-fixtures: internal/testutil is not wired yet (owned by S7/test-scaffolding within this subplan; integrate once it lands)")
}
