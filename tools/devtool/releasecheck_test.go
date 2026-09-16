package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock returns a clock that advances one second per reading, so every step's `seconds` field
// is deterministic and non-zero. time.Now() in a unit test would make the document unassertable.
func fakeClock() func() time.Time {
	base := time.Unix(1_700_000_000, 0).UTC()
	n := 0
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Second)
	}
}

// step builds a step that always answers the given outcome and records that it ran.
func step(name, status string, ran *[]string) releaseCheckStep {
	return releaseCheckStep{name: name, run: func(releaseCheckOptions) releaseCheckOutcome {
		*ran = append(*ran, name)
		return releaseCheckOutcome{Status: status, Detail: name + " detail"}
	}}
}

// TestRunReleaseCheckSteps covers the three properties the gate's value rests on: it stops at the
// first FAIL, a SKIPPED step does not fail the run, and the summary document names every step that
// ran and no step that did not.
func TestRunReleaseCheckSteps(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		wantRan  []string
		wantOK   bool
	}{
		{"all pass", []string{rcPass, rcPass, rcPass}, []string{"a", "b", "c"}, true},
		{"skipped does not fail", []string{rcPass, rcSkipped, rcPass}, []string{"a", "b", "c"}, true},
		{"stops at the first fail", []string{rcPass, rcFail, rcPass}, []string{"a", "b"}, false},
		{"a fail first stops everything", []string{rcFail, rcPass, rcPass}, []string{"a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ran []string
			names := []string{"a", "b", "c"}
			steps := make([]releaseCheckStep, 0, len(names))
			for i, n := range names {
				steps = append(steps, step(n, tc.statuses[i], &ran))
			}
			doc := runReleaseCheckSteps(steps, releaseCheckOptions{Tag: "v1.2.3"}, fakeClock())

			if doc.OK != tc.wantOK {
				t.Errorf("doc.OK = %v, want %v", doc.OK, tc.wantOK)
			}
			if len(ran) != len(tc.wantRan) {
				t.Fatalf("ran %v, want %v", ran, tc.wantRan)
			}
			for i := range ran {
				if ran[i] != tc.wantRan[i] {
					t.Fatalf("ran %v, want %v", ran, tc.wantRan)
				}
			}
			if len(doc.Steps) != len(tc.wantRan) {
				t.Fatalf("the summary holds %d step(s), %d ran", len(doc.Steps), len(tc.wantRan))
			}
			for i, s := range doc.Steps {
				if s.Name != tc.wantRan[i] || s.Status != tc.statuses[i] {
					t.Errorf("step %d = %q/%q, want %q/%q", i, s.Name, s.Status, tc.wantRan[i], tc.statuses[i])
				}
				if s.Seconds <= 0 {
					t.Errorf("step %q recorded %v seconds; a step that ran took time", s.Name, s.Seconds)
				}
			}
		})
	}
}

// TestReleaseCheckSummaryShape pins dist/release-check.json's document: a reader (or a later
// workflow step) must be able to find the version, the tag, every step's status and the verdict.
func TestReleaseCheckSummaryShape(t *testing.T) {
	var ran []string
	steps := []releaseCheckStep{
		step("first", rcPass, &ran),
		step("second", rcSkipped, &ran),
	}
	doc := runReleaseCheckSteps(steps, releaseCheckOptions{Tag: "v9.9.9"}, fakeClock())

	raw, err := marshalBundleJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Version string `json:"version"`
		Tag     string `json:"tag"`
		OK      bool   `json:"ok"`
		Steps   []struct {
			Name    string  `json:"name"`
			Status  string  `json:"status"`
			Seconds float64 `json:"seconds"`
			Detail  string  `json:"detail"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("the summary does not round-trip: %v\n%s", err, raw)
	}
	if back.Tag != "v9.9.9" || back.Version == "" || !back.OK || len(back.Steps) != 2 {
		t.Fatalf("summary = %+v", back)
	}
	if back.Steps[1].Status != rcSkipped || back.Steps[1].Detail == "" {
		t.Errorf("a SKIPPED step must record its reason, got %+v", back.Steps[1])
	}
}

// TestReleaseCheckVersion is ruling R7-3's gate in isolation: no tag is a SKIP, a mismatch is a
// FAIL, and the message says which side to change.
func TestReleaseCheckVersion(t *testing.T) {
	withRepoRoot(t)

	if got := releaseCheckVersion(releaseCheckOptions{}); got.Status != rcSkipped {
		t.Errorf("no tag must SKIP, got %+v", got)
	}
	if got := releaseCheckVersion(releaseCheckOptions{Tag: "1.2.3"}); got.Status != rcFail {
		t.Errorf("a tag without the v prefix must FAIL, got %+v", got)
	}
	got := releaseCheckVersion(releaseCheckOptions{Tag: "v99.99.99"})
	if got.Status != rcFail || !strings.Contains(got.Detail, "core.Version") {
		t.Errorf("a tag disagreeing with core.Version must FAIL and say so, got %+v", got)
	}
}

// TestReleaseCheckStepsAreOrderedAndCoverCILocal pins the gate's composition: every ci-local step
// is present, in ci-local's own order, and the steps survey-release-install.md §10.1 found missing
// come after them.
func TestReleaseCheckStepsAreOrderedAndCoverCILocal(t *testing.T) {
	steps := releaseCheckSteps()
	var names []string
	for _, s := range steps {
		names = append(names, s.name)
	}
	joined := strings.Join(names, "\n")

	if names[0] != "version agreement" {
		t.Errorf("the first step is %q; the version gate is cheap and must run first", names[0])
	}
	at := func(want string) int {
		for i, n := range names {
			if n == want {
				return i
			}
		}
		t.Errorf("release-check has no %q step", want)
		return -1
	}
	prev := -1
	for _, s := range ciLocalSteps {
		i := at("ci-local " + s.name)
		if i <= prev {
			t.Errorf("ci-local step %q is out of ci-local's own order (%d after %d)", s.name, i, prev)
		}
		prev = i
	}
	for _, want := range []string{
		"build-all", "generated docs", "guards", "govulncheck", "licenses",
		"real-binary determinism", "rollback rehearsal", "plugin-validate",
	} {
		if at(want) <= prev {
			t.Errorf("%q must run after the ci-local sequence; release.yml ran ci-local alone and "+
				"that is the gap this gate closes\n%s", want, joined)
		}
	}
}

// TestReleaseCheckRollbackTestsAreNamed guards the hook Task 8 appends to: the rehearsal list must
// never be empty, every entry must name both a package and a test, and the three install
// rehearsal names must be present and exist under ./test/e2e/ (`go test -list`).
func TestReleaseCheckRollbackTestsAreNamed(t *testing.T) {
	all := append(append([]releaseCheckTest{}, releaseCheckRollbackTests...), releaseCheckExtraTests...)
	if len(all) == 0 {
		t.Fatal("the rehearsal list is empty; the gate would pass having rehearsed nothing")
	}
	for _, tc := range all {
		if !strings.HasPrefix(tc.pkg, "./") || tc.run == "" {
			t.Errorf("%+v: pkg must be a ./ package pattern and run a test name", tc)
		}
	}

	want := []releaseCheckTest{
		{pkg: "./test/e2e/", run: "TestInstall_HostCLIInstallUpgradeUninstall"},
		{pkg: "./test/e2e/", run: "TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite"},
		{pkg: "./test/e2e/", run: "TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting"},
	}
	have := map[string]releaseCheckTest{}
	for _, tc := range releaseCheckExtraTests {
		have[tc.run] = tc
	}
	for _, w := range want {
		got, ok := have[w.run]
		if !ok {
			t.Errorf("releaseCheckExtraTests is missing %s", w.run)
			continue
		}
		if got.pkg != w.pkg {
			t.Errorf("%s: pkg=%q, want %q", w.run, got.pkg, w.pkg)
		}
	}

	root, err := findModuleRoot()
	if err != nil {
		t.Fatalf("findModuleRoot: %v", err)
	}
	pattern := "^(" + want[0].run + "|" + want[1].run + "|" + want[2].run + ")$"
	cmd := exec.Command("go", "test", "-count=1", "./test/e2e/", "-list", pattern)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go test ./test/e2e/ -list: %v\n%s", err, out)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); goTestNameRE.MatchString(name) {
			listed[name] = true
		}
	}
	for _, w := range want {
		if !listed[w.run] {
			t.Errorf("./test/e2e/ has no test named %s; go test -run would print ok having run nothing", w.run)
		}
	}
}

// TestReleaseCheckDeterminismVersion pins F13: --tag supplies the shipped version; otherwise
// resolveVersion() does.
func TestReleaseCheckDeterminismVersion(t *testing.T) {
	got, src := releaseCheckDeterminismVersion(releaseCheckOptions{Tag: "v1.2.3"})
	if got != "1.2.3" || src != "--tag" {
		t.Errorf("with --tag v1.2.3: version=%q source=%q, want 1.2.3 / --tag", got, src)
	}
	_, src = releaseCheckDeterminismVersion(releaseCheckOptions{})
	if src != "resolveVersion()" {
		t.Errorf("without --tag: source=%q, want resolveVersion()", src)
	}
}

// TestWriteReleaseCheckDoc_EvidenceCopy writes the summary to a caller-chosen path.
func TestWriteReleaseCheckDoc_EvidenceCopy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "release-check.json")
	doc := releaseCheckDoc{Version: "0.1.0", Tag: "v0.1.0", OK: true}
	if err := writeReleaseCheckDoc(doc, p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"ok": true`) && !strings.Contains(string(b), `"ok":true`) {
		t.Errorf("copied summary missing ok: %s", b)
	}
}
