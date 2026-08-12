package main

// taskTest runs `go test ./...`.
func taskTest(args []string) error {
	return goInherit("test", "./...")
}

// taskTestRace runs `go test -race ./...`.
func taskTestRace(args []string) error {
	return goInherit("test", "-race", "./...")
}
