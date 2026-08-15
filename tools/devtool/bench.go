package main

// taskBench runs every micro-benchmark in the tree: `go test -bench=. -benchmem -run '^$' ./...`.
func taskBench(args []string) error {
	return goInherit("test", "-bench=.", "-benchmem", "-run", "^$", "./...")
}
