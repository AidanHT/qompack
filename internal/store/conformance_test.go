package store_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// This file mechanizes the Rule W-1 merge blocker: that every conformance suite's /behaviour block
// actually RUNS against SP-06's implementations rather than skipping.
//
// The subplan asks for this as `grep -rn "t.Skip" internal/store/storetest …` returning nothing.
// That check is wrong and is deliberately not implemented. Rule W-1 is a RUNTIME probe, not a
// static one: each suite calls its seam (storetest.isStubStore uses PutBytes, redacttest and
// tokenstest use their own probes) and skips only if the implementation still reports
// core.ErrNotImplemented or the documented stub zero value. The literal t.Skip(ruleW1SkipMsg) is
// therefore a PERMANENT fixture of every suite — it simply stops being reached once a real
// implementation lands, and it must keep existing so the suites stay usable against the stubs that
// later waves still ship. A grep would fail forever and prove nothing either way.
//
// What is asserted instead is the property the grep was reaching for: run each suite and confirm
// every /behaviour subtest reports RUN and PASS, and that none reports SKIP.
//
// The mechanism is a `go test -json` subprocess. That is chosen over the alternative of calling the
// suites' own stub probes directly for two reasons: the probes (isStubStore, skipIfStub) are
// unexported and unreachable from here, and — more importantly — probing them would assert that the
// suite WOULD run, whereas this asserts that it DID, which is the thing that actually blocks the
// merge. `-json` rather than `-v` because it is a stable machine-readable contract; scraping "---
// SKIP" out of prose would be exactly the brittleness worth avoiding.

// conformanceTarget is one suite to verify: the package to test, the -run pattern selecting the
// invocations that exercise a REAL implementation, and the /behaviour subtests expected to run.
//
// The -run pattern deliberately excludes each package's *_StubIsSkipped tests, which construct a
// deliberate stub and MUST skip — they are the suites' proof that the W-1 probe still works.
type conformanceTarget struct {
	pkg     string
	run     string
	want    []string
	comment string
}

// conformanceTargets is the three suites SP-06 must clear.
func conformanceTargets() []conformanceTarget {
	return []conformanceTarget{
		{
			pkg: "./internal/store/storetest/",
			run: "TestRunStoreSuite_AgainstRealStore|TestRunSegmentLogSuite_AgainstRealStore",
			want: []string{
				"put_get_round_trip",
				"global_dedup_second_put_is_not_novel",
				"changed_since_detects_a_hash_change",
				"file_history_is_append_only_and_never_shrinks",
				"mark_encoded_is_the_dpi_guard",
				"range_never_loses_a_previously_returned_segment",
			},
			comment: "store.Store and store.SegmentLog against the real FSStore",
		},
		{
			pkg: "./internal/redact/",
			run: "TestRunRedactorSuite_AgainstRealRedactor",
			want: []string{
				"idempotent",
				"bounded_growth",
				"built_in_rules_fire_on_positive_not_negative",
				"rules_lists_active_rule_names",
			},
			comment: "redact.Redactor against redact.New",
		},
		{
			pkg: "./internal/tokens/",
			run: "TestEstimatorSuite_RealImplementation",
			want: []string{
				"monotone_in_length",
				"factor_clamped",
				"estimate_root_sums_chunks",
			},
			comment: "tokens.Estimator against the exact estimator",
		},
	}
}

// TestConformance_BehaviourBlocksActuallyRun is the W-1 merge blocker, mechanized.
func TestConformance_BehaviourBlocksActuallyRun(t *testing.T) {
	root := moduleRootForConformance(t)

	for _, target := range conformanceTargets() {
		t.Run(strings.Trim(strings.ReplaceAll(target.pkg, "/", "_"), "._"), func(t *testing.T) {
			actions := runGoTestJSON(t, root, target.pkg, target.run)

			// A skip is collected at ANY level, not just under /behaviour, and that distinction is
			// load-bearing. RunStoreSuite calls t.Skip in its OWN body — before it ever reaches
			// t.Run(name+"/behaviour", …) — so a stubbed implementation produces a skip on the
			// parent test and NO /behaviour entries whatsoever. Filtering to /behaviour first would
			// make this check unfireable, which is how an assertion ends up looking rigorous and
			// grading nothing.
			var skipped, ran []string
			for name, action := range actions {
				switch {
				case action == "skip":
					skipped = append(skipped, name)
				case action == "pass" && strings.Contains(name, "/behaviour"):
					ran = append(ran, name)
				}
			}
			sort.Strings(skipped)
			sort.Strings(ran)

			require.Empty(t, skipped,
				"%s: a /behaviour subtest SKIPPED, which means the Rule W-1 probe still considers the "+
					"implementation a stub. This is the merge blocker: the conformance suite is the "+
					"specification, and a skipped behaviour block means it never graded anything.\nskipped: %v",
				target.comment, skipped)
			require.NotEmpty(t, ran,
				"%s: no /behaviour subtest ran at all. Either the -run pattern %q matched nothing or the "+
					"suite stopped invoking its behaviour block.", target.comment, target.run)

			// Every named case must be present, so a suite that silently stopped running one of its
			// own assertions is caught rather than passing on a shrunken set.
			for _, want := range target.want {
				found := false
				for _, got := range ran {
					if strings.HasSuffix(got, "/"+want) || strings.Contains(got, "/"+want+"/") {
						found = true
						break
					}
				}
				require.True(t, found,
					"%s: expected behaviour case %q did not run and pass.\nran: %v", target.comment, want, ran)
			}
		})
	}
}

// TestOpen_ExportedConstructor exercises store.Open itself.
//
// Every in-package test deliberately goes through openFS instead (see openOver's comment: they are
// tests of the concrete store and should not couple to the interface). The consequence is that the
// package's ONLY public constructor was covered 0% by internal/store's own profile — the exported
// door that every consumer in the tree actually walks through. This closes that, and pins the two
// properties of Open that openFS cannot have: it returns the Store INTERFACE, and it must not hand
// back a non-nil interface wrapping a nil *FSStore on the error path, which is the classic Go trap
// that would make `if s != nil` lie to every caller.
func TestOpen_ExportedConstructor(t *testing.T) {
	t.Run("returns_a_usable_store", func(t *testing.T) {
		p := testutil.NewProject(t)
		s, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
		require.NoError(t, err)
		require.NotNil(t, s)
		t.Cleanup(func() { _ = s.Close() })

		res, err := s.PutBytes(context.Background(), []byte("exported constructor payload\n"),
			store.PutOptions{Tool: "FileRead", Path: "src/open.ts"})
		require.NoError(t, err)
		require.False(t, res.Root.Hash.IsZero())

		// Segments is documented as never nil, so callers do not nil-check it.
		require.NotNil(t, s.Segments())

		// And RefCounter, the narrow seam SP-14 reaches ApproxRefs through rather than type-asserting
		// on *FSStore.
		rc, ok := s.(store.RefCounter)
		require.True(t, ok, "the store returned by Open must satisfy store.RefCounter")
		require.Positive(t, rc.ApproxRefs(res.Root.Chunks[0].Hash),
			"a chunk that was just stored must have a non-zero approximate refcount")
	})

	t.Run("error_path_returns_a_truly_nil_interface", func(t *testing.T) {
		// A file where the project root must be: EnsureLayout cannot create .qompack under it, so
		// Open fails.
		dir := t.TempDir()
		notADir := filepath.Join(dir, "blocked")
		require.NoError(t, os.WriteFile(notADir, []byte("not a directory"), 0o600))

		s, err := store.Open(notADir, testutil.NewProject(t).Cfg, store.Deps{})
		require.Error(t, err, "Open must fail when the project root cannot hold a .qompack tree")
		require.Nil(t, s,
			"Open must return a genuinely nil Store on failure. Returning openFS's result directly "+
				"would wrap a nil *FSStore in a non-nil interface, and every `if s != nil` in the tree "+
				"would silently be wrong")
	})
}

// runGoTestJSON runs `go test -json` for one package and returns the terminal action recorded for
// each test name ("pass", "fail", "skip").
func runGoTestJSON(t *testing.T, root, pkg, run string) map[string]string {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), "go", "test", "-json", "-count=1", "-run", run, pkg)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A non-zero exit is not fatal here: the per-test actions below are the assertion, and a
	// clearer failure comes from them than from an exit code.
	_ = cmd.Run()

	actions := map[string]string{}
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Test == "" {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			actions[ev.Test] = ev.Action
		}
	}
	require.NoError(t, sc.Err())
	require.NotEmpty(t, actions,
		"go test -json produced no test events for %s (-run %q)\nstderr:\n%s", pkg, run, stderr.String())
	return actions
}

// moduleRootForConformance returns the repository root, so the subprocess resolves ./internal/...
// package patterns the same way a developer typing them would.
func moduleRootForConformance(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for d := wd; ; {
		if fi, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil && fi.Mode().IsRegular() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatal(fmt.Sprintf("no go.mod found in %s or any parent", wd))
		}
		d = parent
	}
}
