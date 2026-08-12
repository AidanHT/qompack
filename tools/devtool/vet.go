package main

// taskVet runs `go vet ./...`.
func taskVet(args []string) error {
	return goInherit("vet", "./...")
}
