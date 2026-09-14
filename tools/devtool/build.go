package main

import (
	"path/filepath"
	"runtime"
)

// taskBuild builds the single static binary for the host platform, through the one build
// invocation goBuildRelease owns (00-ARCHITECTURE.md §2.6).
//
// The output name carries exeSuffix for the same reason taskBuildAll does. `go build -o bin/qompack`
// on Windows produces a file with no extension, which the shell will not execute — so every
// downstream step that runs bin/qompack (`devtool test-e2e`, the plugin harness, a developer
// following the README) fails on Windows only, with an error about the command not existing rather
// than about the build.
func taskBuild(args []string) error {
	version := resolveVersion()
	out := buildOutputPath(runtime.GOOS)
	return goBuildRelease("", "", out, versionLdflags(version))
}

// goBuildRelease is the one `go build` invocation every qompack binary this repository produces
// goes through: `build`, `build-all` and `bundle`. It is a shared helper rather than three copies
// of the flag list because a flag present in one and missing from another is invisible until
// something downstream depends on it — which is exactly what happened to the .exe suffix
// buildOutputPath now owns.
//
// The flags beyond §2.6's CGO_ENABLED=0/-trimpath/-ldflags are both determinism flags, added by
// SP-17 because a bundle's checksums.txt is only meaningful if two builds of the same source
// produce the same bytes:
//
//   - -buildvcs=false keeps the VCS stamp (commit, and a dirty bit that moves with any unrelated
//     edit in the worktree) out of the binary. The commit is recorded in BUNDLE.json instead,
//     where it is data rather than an input to the hash of every file beside it.
//   - -buildid= (inside the ldflags, see versionLdflags) clears the build ID, which otherwise
//     varies with the action graph.
func goBuildRelease(goos, goarch, out, ldflags string) error {
	return goInheritEnv(goBuildEnv(goos, goarch), goBuildArgs(out, ldflags)...)
}

// goBuildArgs is the argv, and goBuildEnv the environment, of that one invocation. They are split
// out from goBuildRelease so the flag list is assertable without shelling out to a real compile:
// -trimpath and -buildvcs=false are load-bearing for the determinism packaging/README.md §4 and
// every BUNDLE.json assert, yet they live in an argv slice that no test could otherwise read — and
// a suite that cannot see a flag stays green when somebody deletes it.
func goBuildArgs(out, ldflags string) []string {
	return []string{"build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", out, "./cmd/qompack"}
}

// goBuildEnv pins CGO off (§2.6's static-binary requirement) and cross-compiles when a target is
// named. An empty goos/goarch leaves GOOS/GOARCH inherited rather than set to the host's own
// values, so `go env` decides what "host" means.
func goBuildEnv(goos, goarch string) map[string]string {
	env := map[string]string{"CGO_ENABLED": "0"}
	if goos != "" {
		env["GOOS"] = goos
	}
	if goarch != "" {
		env["GOARCH"] = goarch
	}
	return env
}

// buildOutputPath is the host-platform binary path `build` writes. Split out from taskBuild so the
// platform suffix is assertable without shelling out to a real compile.
func buildOutputPath(goos string) string {
	return filepath.Join("bin", "qompack"+exeSuffix(goos))
}
