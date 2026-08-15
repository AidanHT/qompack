package cli

import "os"

// currentDir is os.Getwd behind a name, so the one place cli reads ambient process state is
// obvious and easy to find. Hook subcommands never call it: they take their working directory from
// the payload (§3.3 resolution order), because the host's cwd and the project root are not always
// the same thing.
func currentDir() (string, error) { return os.Getwd() }

// isDir reports whether p exists and is a directory. Hooks use it to refuse to create a store
// under a project root that does not exist; see the resolution step in runHook.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
