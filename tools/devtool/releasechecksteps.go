package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// releaseCheckTest is one `go test -run` the rollback-rehearsal step executes. Task 8 appends its
// own end-to-end rehearsal names to releaseCheckExtraTests once they exist, which is why the step
// reads two slices rather than one literal list.
type releaseCheckTest struct {
	pkg string
	run string
}

// releaseCheckRollbackTests is the store-level rollback rehearsal: the drill that runs before and
// after the first new-format write, the proof that a restored backup opens as a real store, and
// the proof that verification detects tampering. Together they are what "rollback rehearsal
// attached" (SP17-M7-08) means today — there is no CLI surface for the migration/rollback API, so
// the package's own tests ARE the rehearsal.
var releaseCheckRollbackTests = []releaseCheckTest{
	{pkg: "./internal/store/", run: "TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite"},
	{pkg: "./internal/store/", run: "TestBackup_RestoreOpensAsARealStore"},
	{pkg: "./internal/store/", run: "TestBackup_VerifyDetectsTamperingAndSizeDrift"},
}

// releaseCheckExtraTests lists additional `go test -run` names step 9 executes; Task 8 appends here.
var releaseCheckExtraTests = []releaseCheckTest{
	{pkg: "./test/e2e/", run: "TestInstall_HostCLIInstallUpgradeUninstall"},
	{pkg: "./test/e2e/", run: "TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite"},
	{pkg: "./test/e2e/", run: "TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting"},
}

// releaseCheckSteps is the ordered gate.
func releaseCheckSteps() []releaseCheckStep {
	steps := []releaseCheckStep{{name: "version agreement", run: releaseCheckVersion}}
	// ci-local's own list, reused rather than copied: a step added there is gated here for free,
	// and the two can never disagree about what "the local CI sequence" means.
	for _, s := range ciLocalSteps {
		steps = append(steps, releaseCheckStep{
			name: "ci-local " + s.name,
			run:  func(releaseCheckOptions) releaseCheckOutcome { return rcTask(s.fn, s.args...) },
		})
	}
	return append(steps,
		releaseCheckStep{"build-all", func(releaseCheckOptions) releaseCheckOutcome { return rcTask(taskBuildAll) }},
		releaseCheckStep{"generated docs", func(releaseCheckOptions) releaseCheckOutcome {
			if out := rcTask(taskGenMCPDocs, "--check"); out.Status == rcFail {
				return out
			}
			return rcTask(taskGenCommandDocs, "--check")
		}},
		releaseCheckStep{"guards", func(releaseCheckOptions) releaseCheckOutcome {
			return rcGo("test", "-count=1", "./test/guards/")
		}},
		releaseCheckStep{"govulncheck", releaseCheckVulncheck},
		releaseCheckStep{"licenses", func(releaseCheckOptions) releaseCheckOutcome {
			return rcTask(taskLicenses, "--check")
		}},
		releaseCheckStep{"real-binary determinism", releaseCheckDeterminism},
		releaseCheckStep{"rollback rehearsal", releaseCheckRollback},
		releaseCheckStep{"plugin-validate", func(releaseCheckOptions) releaseCheckOutcome {
			return rcTask(taskPluginValidate)
		}},
	)
}

// rcTask runs a devtool task as one gate step.
func rcTask(fn func([]string) error, args ...string) releaseCheckOutcome {
	if err := fn(args); err != nil {
		return rcFailf("%v", err)
	}
	return rcPassf("")
}

// rcGo runs `go <args…>` with inherited stdio as one gate step.
func rcGo(args ...string) releaseCheckOutcome {
	if err := goInherit(args...); err != nil {
		return rcFailf("go %s: %v", strings.Join(args, " "), err)
	}
	return rcPassf("go %s", strings.Join(args, " "))
}

// releaseCheckVersion is ruling R7-3: the tag and internal/core.Version must already agree, and
// git must already believe HEAD carries that tag.
//
// It is deliberately a GATE rather than a fix-up. core.Version is `0.1.0` while the newest tag is
// `v0.2.0`, so the next release fails here until somebody bumps the constant on purpose — which is
// the point. A release tool that quietly stamped whatever the tag said would let a binary report a
// version no commit in this repository ever declared.
func releaseCheckVersion(o releaseCheckOptions) releaseCheckOutcome {
	if o.Tag == "" {
		return rcSkipf("no tag: pass --tag vX.Y.Z to gate the tag/core.Version agreement")
	}
	want, ok := strings.CutPrefix(o.Tag, "v")
	if !ok {
		return rcFailf("tag %q does not start with v; release tags are vX.Y.Z", o.Tag)
	}
	if want != core.Version {
		return rcFailf("tag %q names version %q but internal/core.Version is %q; bump core.Version "+
			"in its own commit before tagging", o.Tag, want, core.Version)
	}
	stdout, stderr, err := runCapture(nil, "git", "describe", "--tags", "--exact-match")
	if err != nil {
		return rcFailf("`git describe --tags --exact-match` failed, so HEAD carries no tag: %v\n%s", err, stderr)
	}
	if got := strings.TrimSpace(string(stdout)); got != o.Tag {
		return rcFailf("HEAD's exact tag is %q, not %q", got, o.Tag)
	}
	return rcPassf("tag %s, internal/core.Version %s, HEAD's exact tag agrees", o.Tag, core.Version)
}

// releaseCheckVulncheck runs the same invocation ci.yml's security job uses.
func releaseCheckVulncheck(o releaseCheckOptions) releaseCheckOutcome {
	if o.SkipVulncheck {
		return rcSkipf("--skip-vulncheck: the vulnerability database was not consulted on this run")
	}
	if err := pinnedRunInherit(govulncheckPkg, "./..."); err != nil {
		return rcFailf("govulncheck ./...: %v", err)
	}
	return rcPassf("govulncheck ./... reported nothing")
}

// releaseCheckDeterminism assembles the host target twice with the REAL builder and compares every
// file. The unit tests prove the assembler is deterministic over a fixture binary; only this step
// proves the compiler is, which is the half `checksums.txt` actually depends on.
func releaseCheckDeterminism(o releaseCheckOptions) releaseCheckOutcome {
	host := bundleTarget{OS: runtime.GOOS, Arch: runtime.GOARCH}
	version, versionSrc := releaseCheckDeterminismVersion(o)
	dirs := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		base, err := os.MkdirTemp("", "qompack-determinism-")
		if err != nil {
			return rcFailf("creating a temporary directory: %v", err)
		}
		defer func() { _ = os.RemoveAll(base) }()
		asm := bundleAssembly{
			version:   version,
			source:    gitSource(),
			goVersion: runtime.Version(),
			outDir:    base,
			build: func(goos, goarch, out string) error {
				return goBuildRelease(goos, goarch, out, versionLdflags(version))
			},
		}
		dir, _, err := asm.assemble(host)
		if err != nil {
			return rcFailf("assembling %s/%s (pass %d): %v", host.OS, host.Arch, i+1, err)
		}
		dirs = append(dirs, dir)
	}
	if diff := compareBundleTrees(dirs[0], dirs[1]); diff != "" {
		return rcFailf("two assemblies of %s at version %s differ: %s", host.OS+"/"+host.Arch, version, diff)
	}
	return rcPassf("two real-binary assemblies of %s at version %s are byte-identical (version from %s)",
		host.OS+"/"+host.Arch, version, versionSrc)
}

// releaseCheckDeterminismVersion is the version the determinism step stamps: the --tag-derived
// version when --tag is given (the one a release ships), resolveVersion() otherwise.
func releaseCheckDeterminismVersion(o releaseCheckOptions) (version, source string) {
	if o.Tag != "" {
		want, _ := strings.CutPrefix(o.Tag, "v")
		return bundleVersion(want, "", core.Version), "--tag"
	}
	return bundleVersion("", resolveVersion(), core.Version), "resolveVersion()"
}

// compareBundleTrees returns "" when two assembled bundles hold the same files with the same
// bytes, and otherwise the first difference found.
func compareBundleTrees(a, b string) string {
	fa, err := hashBundleTreeIncludingIdentity(a)
	if err != nil {
		return err.Error()
	}
	fb, err := hashBundleTreeIncludingIdentity(b)
	if err != nil {
		return err.Error()
	}
	names := map[string]bool{}
	for n := range fa {
		names[n] = true
	}
	for n := range fb {
		names[n] = true
	}
	var diffs []string
	for _, n := range sortedKeys(names) {
		switch {
		case fa[n] == "":
			diffs = append(diffs, n+" only in the second assembly")
		case fb[n] == "":
			diffs = append(diffs, n+" only in the first assembly")
		case fa[n] != fb[n]:
			diffs = append(diffs, n+" differs")
		}
	}
	return strings.Join(diffs, "; ")
}

// hashBundleTreeIncludingIdentity is hashBundleTree plus BUNDLE.json and checksums.txt, which the
// assembler's own listing excludes because it cannot describe itself. Here they must be compared:
// an identity document that differed between two assemblies would be the loudest possible
// determinism failure and the one hashBundleTree cannot see.
func hashBundleTreeIncludingIdentity(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		sum, _, hashErr := hashFile(p)
		if hashErr != nil {
			return hashErr
		}
		out[filepath.ToSlash(rel)] = sum
		return nil
	})
	return out, err
}

// goTestNameRE matches one name of `go test -list`'s output, which also carries an "ok …" line.
var goTestNameRE = regexp.MustCompile(`^(Test|Benchmark|Fuzz|Example)\w*$`)

// releaseCheckRollback runs the rehearsal tests, after confirming with `-list` that every name it
// is about to ask for exists.
//
// The confirmation is the whole point of the step's shape. `go test -run` prints `ok` when its
// pattern matches nothing, so a renamed or deleted rehearsal would turn this gate green while
// rehearsing nothing at all — which is precisely the failure mode "no zero-test run counts as a
// pass" was written against.
func releaseCheckRollback(releaseCheckOptions) releaseCheckOutcome {
	byPkg := map[string][]string{}
	for _, t := range append(append([]releaseCheckTest{}, releaseCheckRollbackTests...), releaseCheckExtraTests...) {
		byPkg[t.pkg] = append(byPkg[t.pkg], t.run)
	}
	if len(byPkg) == 0 {
		return rcFailf("no rehearsal tests are configured; the gate would pass vacuously")
	}
	var ran []string
	for _, pkg := range sortedKeys(toSet(byPkg)) {
		names := byPkg[pkg]
		sort.Strings(names)
		pattern := "^(" + strings.Join(names, "|") + ")$"
		stdout, stderr, err := runCapture(nil, "go", "test", "-count=1", pkg, "-list", pattern)
		if err != nil {
			return rcFailf("go test %s -list: %v\n%s", pkg, err, stderr)
		}
		listed := map[string]bool{}
		for _, line := range strings.Split(string(stdout), "\n") {
			if name := strings.TrimSpace(line); goTestNameRE.MatchString(name) {
				listed[name] = true
			}
		}
		for _, n := range names {
			if !listed[n] {
				return rcFailf("%s has no test named %s; `go test -run` would print ok having run "+
					"nothing", pkg, n)
			}
		}
		if err := goInherit("test", "-count=1", pkg, "-run", pattern, "-v"); err != nil {
			return rcFailf("go test %s -run %s: %v", pkg, pattern, err)
		}
		ran = append(ran, fmt.Sprintf("%s (%d)", pkg, len(names)))
	}
	return rcPassf("rehearsed %s", strings.Join(ran, ", "))
}

// toSet turns a map's keys into the set sortedKeys consumes.
func toSet[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
