package main

import "fmt"

// ciLocalStep is one stage of `devtool ci-local`.
type ciLocalStep struct {
	name string
	fn   func([]string) error
	args []string
}

// ciLocalSteps runs in exactly the order the implementation spec's task table gives:
// fmt-check -> lint -> vet -> build -> test -> cover -> plugin-validate -> gen-config-docs (check mode).
var ciLocalSteps = []ciLocalStep{
	{"fmt-check", taskFmtCheck, nil},
	{"lint", taskLint, nil},
	{"vet", taskVet, nil},
	{"build", taskBuild, nil},
	{"test", taskTest, nil},
	{"cover", taskCover, nil},
	{"plugin-validate", taskPluginValidate, nil},
	{"gen-config-docs", taskGenConfigDocs, []string{"--check"}},
}

// taskCILocal runs the whole local CI sequence, stopping at the first step that fails.
func taskCILocal(args []string) error {
	for _, step := range ciLocalSteps {
		fmt.Printf("\n=== ci-local: %s ===\n", step.name)
		if err := step.fn(step.args); err != nil {
			return fmt.Errorf("ci-local: step %q failed: %w", step.name, err)
		}
	}
	return nil
}
