// Command devtool is Qompack's task runner (00-ARCHITECTURE.md §2.6): one Go program, invoked as
// `go run ./tools/devtool <task> [args...]` from the repository root, identical on PowerShell,
// bash and zsh. There is deliberately no Makefile-only workflow, because the primary dev machine
// is Windows.
//
// devtool is a package of the root module (not a nested module) specifically so that
// gen-config-docs can import internal/config once it exists; the pinned external tool binaries
// (gofumpt, golangci-lint, govulncheck, benchstat) live in the separate nested module
// tools/pinned and are always invoked as `go run -modfile=tools/pinned/go.mod <import/path>`.
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// root is the repository root: the directory containing the main module's go.mod. It is resolved
// once in main and every task assumes the process's current directory is root.
var root string

// tasks maps each canonical task name (00-ARCHITECTURE.md §2.6) to its implementation. Every
// entry takes the task's remaining command-line arguments and returns a non-nil error to signal
// failure; main turns that into a process exit code.
var tasks = map[string]func(args []string) error{
	"fmt":                   taskFmt,
	"fmt-check":             taskFmtCheck,
	"lint":                  taskLint,
	"vet":                   taskVet,
	"build":                 taskBuild,
	"build-all":             taskBuildAll,
	"test":                  taskTest,
	"test-race":             taskTestRace,
	"cover":                 taskCover,
	"bench":                 taskBench,
	"bench-compare":         taskBenchCompare,
	"bench-hotpath":         taskBenchHotpath,
	"replay":                taskReplay,
	"plugin-validate":       taskPluginValidate,
	"fsck":                  taskFsck,
	"ci-local":              taskCILocal,
	"gen-config-docs":       taskGenConfigDocs,
	"gen-contract-fixtures": taskGenContractFixtures,
	"gen-fixtures":          taskGenFixtures,
	"install-hooks":         taskInstallHooks,
	"check-commit-msg":      taskCheckCommitMsg,
}

func main() {
	os.Exit(run(os.Args[1:]))
}

// run implements main's logic and returns a process exit code, so tests can exercise it without
// calling os.Exit.
func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: devtool <task> [args...]")
		printTaskList(os.Stderr)
		return 2
	}
	task, rest := args[0], args[1:]
	fn, ok := tasks[task]
	if !ok {
		fmt.Fprintf(os.Stderr, "devtool: unknown task %q\n", task)
		printTaskList(os.Stderr)
		return 2
	}

	r, err := findModuleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "devtool:", err)
		return 1
	}
	root = r
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "devtool: chdir to module root:", err)
		return 1
	}

	if err := fn(rest); err != nil {
		if errors.Is(err, errUsage) {
			return 2
		}
		fmt.Fprintf(os.Stderr, "devtool: %s: %v\n", task, err)
		return 1
	}
	return 0
}

// errUsage marks an error as a command-line usage mistake (bad flags), which exits 2 instead of
// the generic 1 used for a task that ran and failed.
var errUsage = errors.New("usage error")

func printTaskList(w *os.File) {
	fmt.Fprintln(w, "tasks:")
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %s\n", name)
	}
}
