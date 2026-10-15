package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// modStageAAllowlist is every host API call the Stage A mod may make: its own binary through
// $.process.run, the session's id, root and version, the plugin root, and $.ui.log. A call outside
// it fails mod-validate; widening it is a reviewed change to this list.
var modStageAAllowlist = []string{
	"$.process.run",
	"$.session.id",
	"$.session.root",
	"$.session.version",
	"$.plugin.root",
	"$.ui.log",
}

// validateReport is the part of `claude plugin validate --json` mod-validate reads. Success is a
// pointer so a report without the key reads as no verdict rather than as false. The issue lists
// are kept raw: they are shown, never interpreted.
type validateReport struct {
	Success  *bool             `json:"success"`
	Manifest validateSection   `json:"manifest"`
	Contents []validateSection `json:"contents"`
}

type validateSection struct {
	File        string               `json:"file"`
	Type        string               `json:"type"`
	Errors      []json.RawMessage    `json:"errors"`
	Warnings    []json.RawMessage    `json:"warnings"`
	Notes       []string             `json:"notes"`
	GatingHooks []validateGatingHook `json:"gatingHooks"`
}

// validateGatingHook is one gatingHooks entry: a hook registered at a site that can refuse an
// action, and whether it has a .catch handler.
type validateGatingHook struct {
	Module   string `json:"module"`
	Pattern  string `json:"pattern"`
	Hook     string `json:"hook"`
	HasCatch bool   `json:"hasCatch"`
}

func parseValidateReport(stdout []byte) (validateReport, error) {
	var r validateReport
	if err := json.Unmarshal(stdout, &r); err != nil {
		return validateReport{}, fmt.Errorf("its output is not the JSON report --json promises: %w", err)
	}
	return r, nil
}

func (r validateReport) sections() []validateSection {
	return append([]validateSection{r.Manifest}, r.Contents...)
}

// validateCallsNote is the line the validator prints for a module's host calls, e.g.
// "./register.ts calls: $.process.run (via hello), $.ui.log". On 2.1.300 this text is the only
// place the call list appears; --json has no key for it.
var validateCallsNote = regexp.MustCompile(`^(\S+) calls: (.*)$`)

// validateNoCalls is the body of the calls line for a module that makes no host call.
const validateNoCalls = "nothing on $"

// validateCallEntry is one entry of the calls line: a full `$` call, optionally followed by the
// top-level function it is made through.
var validateCallEntry = regexp.MustCompile(`^(\$(?:\.[A-Za-z_$][\w$]*)+)(?: \(via [^()]+\))?$`)

// calls returns the host calls the report lists, sorted and deduplicated, and whether any calls
// line was found at all. An entry it cannot read is an error: an allowlist check that skipped it
// would pass a call nobody looked at.
func (r validateReport) calls() ([]string, bool, error) {
	found := false
	set := map[string]bool{}
	for _, sec := range r.sections() {
		for _, note := range sec.Notes {
			m := validateCallsNote.FindStringSubmatch(note)
			if m == nil {
				continue
			}
			found = true
			body := strings.TrimSpace(m[2])
			if body == validateNoCalls {
				continue
			}
			if body == "" {
				return nil, true, fmt.Errorf("the calls line %q lists nothing", note)
			}
			for _, entry := range strings.Split(body, ", ") {
				e := validateCallEntry.FindStringSubmatch(strings.TrimSpace(entry))
				if e == nil {
					return nil, true, fmt.Errorf("cannot read the entry %q of the calls line %q", entry, note)
				}
				set[e[1]] = true
			}
		}
	}
	if !found {
		return nil, false, nil
	}
	return sortedKeys(set), true, nil
}

// gatingHooks returns every gatingHooks entry in the report, manifest first.
func (r validateReport) gatingHooks() []validateGatingHook {
	var out []validateGatingHook
	for _, sec := range r.sections() {
		out = append(out, sec.GatingHooks...)
	}
	return out
}

// moduleCallUse matches a host API use in module source: `$`, not preceded by an identifier
// character, then one or more `.name` segments, with optional chaining and spacing allowed.
var moduleCallUse = regexp.MustCompile(`(?:^|[^\w$])\$((?:\s*\??\.\s*[A-Za-z_$][\w$]*)+)`)

var moduleCallSpace = regexp.MustCompile(`[\s?]+`)

// scanModuleCalls lists the host API uses in a module's source, sorted and deduplicated. It is
// the fallback when the validator's output lists no calls, and it fails closed: a use in a comment
// or a string counts, and a deeper member access counts as its full path.
func scanModuleCalls(src string) []string {
	set := map[string]bool{}
	for _, m := range moduleCallUse.FindAllStringSubmatch(src, -1) {
		set["$"+moduleCallSpace.ReplaceAllString(m[1], "")] = true
	}
	return sortedKeys(set)
}

// unlistedModCalls returns the calls outside modStageAAllowlist, in the order given.
func unlistedModCalls(calls []string) []string {
	allowed := make(map[string]bool, len(modStageAAllowlist))
	for _, a := range modStageAAllowlist {
		allowed[a] = true
	}
	var out []string
	for _, c := range calls {
		if !allowed[c] {
			out = append(out, c)
		}
	}
	return out
}

func taskModValidate(args []string) error {
	fs := flag.NewFlagSet("mod-validate", flag.ContinueOnError)
	record := fs.String("record", "",
		"also write the validator's JSON output, with the plugin directory replaced by <out>, to this file")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	if fs.NArg() > 0 {
		return errors.Join(errUsage, fmt.Errorf("mod-validate: unexpected argument %q", fs.Arg(0)))
	}
	if err := rejectEvidenceDirectoryPath(*record); err != nil {
		return errors.Join(errUsage, err)
	}
	return runModValidate(osModClaude(), root, runtime.GOOS, *record, os.Stdout)
}

// runModValidate materialises goos's mods plugin (a relative recordPath is under repoRoot), runs `claude plugin validate --strict --json` on
// it and asserts the Stage A allowlist. It fails closed: without claude, without exit 0 and
// success:true, on a calls line it cannot read, and on any call outside the allowlist. When the
// output lists no calls it scans the materialised hooks/register.ts instead. The gating hooks are
// reported, not judged.
func runModValidate(c modClaude, repoRoot, goos, recordPath string, w io.Writer) (err error) {
	const task = "mod-validate"
	bin, err := lookUpModClaude(c, task)
	if err != nil {
		return err
	}
	ws, err := newModWorkspace(goos)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, ws.close()) }()

	env := modClaudeEnv(os.Environ(), ws.configDir)
	printClaudeVersion(c, env, bin, task, w)
	run := c.run(env, bin, "plugin", "validate", ws.plugin, "--strict", "--json")
	if run.Err != nil {
		return fmt.Errorf("`claude plugin validate` gave no verdict: %w", run.Err)
	}
	redact := hostPathRedactor(repoRoot, ws.plugin, userHomeDir())
	if s := strings.TrimSpace(redact(string(run.Stderr))); s != "" {
		_, _ = fmt.Fprintf(w, "%s: claude stderr: %s\n", task, s)
	}
	if recordPath != "" {
		dst := recordPath
		if !filepath.IsAbs(dst) {
			dst = filepath.Join(repoRoot, filepath.FromSlash(dst))
		}
		if err := writeModFile(dst, []byte(redact(string(run.Stdout)))); err != nil {
			return fmt.Errorf("recording the validator output: %w", err)
		}
		_, _ = fmt.Fprintf(w, "%s: validator output recorded to %s\n", task, recordPath)
	}

	report, err := parseValidateReport(run.Stdout)
	if err != nil {
		return fmt.Errorf("`claude plugin validate --json` (exit %d): %w", run.ExitCode, err)
	}
	if err := validateVerdict(report, run.ExitCode, redact); err != nil {
		return err
	}

	calls, found, err := report.calls()
	if err != nil {
		return err
	}
	source := "the validator's calls line"
	if !found {
		reg, readErr := os.ReadFile(filepath.Join(ws.plugin, "hooks", "register.ts"))
		if readErr != nil {
			return fmt.Errorf("the validator listed no calls, and hooks/register.ts could not be read: %w", readErr)
		}
		calls = scanModuleCalls(string(reg))
		source = "scanned hooks/register.ts (the validator listed no calls)"
	}
	_, _ = fmt.Fprintf(w, "%s: calls: %s (from %s)\n", task, listOrNone(calls), source)

	gating := report.gatingHooks()
	if len(gating) == 0 {
		_, _ = fmt.Fprintf(w, "%s: gating hooks: none\n", task)
	}
	for _, g := range gating {
		catch := "no .catch"
		if g.HasCatch {
			catch = "has .catch"
		}
		_, _ = fmt.Fprintf(w, "%s: gating hook %s (%s): %s\n", task, g.Hook, g.Module, catch)
	}

	if bad := unlistedModCalls(calls); len(bad) > 0 {
		return fmt.Errorf("the mod calls %s, which the Stage A allowlist (modStageAAllowlist in "+
			"tools/devtool/modvalidate.go) does not list", strings.Join(bad, ", "))
	}
	_, _ = fmt.Fprintf(w, "%s: OK (validator passed --strict; every call is on the Stage A allowlist)\n", task)
	return nil
}

// validateVerdict passes only when the CLI exited 0 AND reported success:true; the exit status is
// never overridden by the payload, nor the payload by the exit status.
func validateVerdict(r validateReport, exit int, redact func(string) string) error {
	if exit == 0 && r.Success != nil && *r.Success {
		return nil
	}
	var issues []string
	for _, sec := range r.sections() {
		for _, raw := range append(append([]json.RawMessage(nil), sec.Errors...), sec.Warnings...) {
			var compact bytes.Buffer
			if json.Compact(&compact, raw) != nil {
				compact.Reset()
				compact.Write(raw)
			}
			issues = append(issues, redact(compact.String()))
		}
	}
	success := "no success key"
	if r.Success != nil {
		success = fmt.Sprintf("success:%t", *r.Success)
	}
	return fmt.Errorf("`claude plugin validate --strict` did not pass the plugin (exit %d, %s): %s",
		exit, success, listOrNone(issues))
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
