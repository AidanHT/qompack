package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/cli"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// E2EBinaryEnv selects (*Project).RunHook's mode. Unset — the default — runs the hook in-process
// through cli.Dispatch, which is fast enough to use in every unit test. Set to the path of a
// built qompack binary, it spawns that binary instead. 00-ARCHITECTURE.md §6.2 mandates both
// ("real binary or in-proc"); test/e2e sets it from the binary its own harness builds.
const E2EBinaryEnv = "QOMPACK_E2E_BINARY"

// filePerm is the mode WithFiles creates a project source file with, and dirPerm the mode it
// creates the directories leading to it with. Source files a test writes are ordinary files: the
// 0o700/0o600 the runtime store uses applies to .qompack/, not to the project's own tree.
const (
	filePerm = 0o600
	dirPerm  = 0o700
)

// Project is a disposable Qompack project on disk: a real directory tree with a real .qompack/
// layout, a real configuration loaded through the real five-layer pipeline, a real file-backed
// logger, and a clock that only moves when the test moves it.
//
// Every path it owns lives under t.TempDir(), including the user-global home the config loader
// and paths.Global see, so no test can reach the developer's own ~/.qompack (00-ARCHITECTURE.md
// §13 invariant 7).
type Project struct {
	// Root is the project root: the directory containing .qompack/.
	Root string
	// Cfg is the configuration as the five-layer pipeline resolved it for this project.
	Cfg config.Config
	// Clock is the project's time seam, frozen at Epoch unless WithClock says otherwise.
	Clock *FakeClock
	// Log writes to <root>/.qompack/logs/. Its closer is registered with t.Cleanup by NewProject.
	Log logging.Logger

	// home is the user-global layer's root, also under t.TempDir(): paths.Global(home) is
	// <home>/.qompack.
	home string
	// env is the overlay Getenv serves before falling back to the process environment.
	env map[string]string
	// seam is the set of write primitives AssertAppendOnly drives. It is a field, not a set of
	// direct calls, so that TestProject_AssertAppendOnly can substitute deliberately weakened
	// primitives and prove the assertion actually fails when the invariant stops holding.
	seam writeSeam
}

// writeSeam is the three write primitives AssertAppendOnly exercises, as values.
//
// In production use these are exactly paths.OpenFile, paths.WriteAtomic and paths.CreateNew — the
// three doors §7.4 puts the append-only guard behind. Naming them as a struct costs nothing at
// run time and buys the one thing an assertion like this otherwise cannot have: a test that
// proves it fails when the guard is gone.
type writeSeam struct {
	OpenFile    func(p string, flag int, perm fs.FileMode) (*os.File, error)
	WriteAtomic func(p string, b []byte, perm fs.FileMode) error
	CreateNew   func(p string, b []byte) error
}

// productionSeam returns the real §7.4 write primitives.
func productionSeam() writeSeam {
	return writeSeam{
		OpenFile:    paths.OpenFile,
		WriteAtomic: paths.WriteAtomic,
		CreateNew:   paths.CreateNew,
	}
}

// ProjectOpt configures NewProject. Options are applied in the order given, before the layout is
// created, so an option may rely on the project root existing but not on .qompack/ existing.
type ProjectOpt func(*projectOpts)

// projectOpts accumulates what the options asked for. It is separate from Project because these
// are inputs to construction, not state a test should read back afterwards.
type projectOpts struct {
	git     bool
	cfgJSON string
	hasCfg  bool
	env     map[string]string
	files   map[string]string
	clock   time.Time
}

// WithGit makes NewProject run `git init` in the project root, so paths.Resolve reaches the root
// through its .git walk (00-ARCHITECTURE.md §3.3 step 2) rather than only through
// QOMPACK_PROJECT_ROOT. Without it the project deliberately has no .git, which exercises step 3.
func WithGit() ProjectOpt {
	return func(o *projectOpts) { o.git = true }
}

// WithConfig writes jsonText to <root>/.qompack/config.json before configuration is loaded, so
// the project file layer of the §11.2 precedence chain is populated. The text is written
// verbatim, including deliberately invalid JSON or out-of-range values: §11.3's fallback-not-crash
// behaviour is a thing tests need to be able to provoke.
func WithConfig(jsonText string) ProjectOpt {
	return func(o *projectOpts) { o.cfgJSON, o.hasCfg = jsonText, true }
}

// WithEnv sets k to v for the lifetime of the test, in both the process environment (via
// t.Setenv, so a spawned binary inherits it) and the Project's own Getenv overlay.
func WithEnv(k, v string) ProjectOpt {
	return func(o *projectOpts) {
		if o.env == nil {
			o.env = map[string]string{}
		}
		o.env[k] = v
	}
}

// WithFiles creates project source files at construction time. Keys are slash-separated paths
// relative to the project root; values are file contents written verbatim, so a fixture may carry
// CRLF, a BOM, or anything else the test needs to see survive.
func WithFiles(files map[string]string) ProjectOpt {
	return func(o *projectOpts) {
		if o.files == nil {
			o.files = map[string]string{}
		}
		for k, v := range files {
			o.files[k] = v
		}
	}
}

// WithClock starts the project's FakeClock at t0 instead of Epoch.
func WithClock(t0 time.Time) ProjectOpt {
	return func(o *projectOpts) { o.clock = t0 }
}

// NewProject builds a Project under t.TempDir().
//
// The construction order is fixed and each step depends on the one before it: create the root and
// an isolated home, apply the options (which may git init it and populate it with source files),
// point QOMPACK_PROJECT_ROOT/HOME/USERPROFILE at those temporary directories, create the .qompack/
// layout, write the project config file if one was requested, open the file-backed logger and
// register its close with t.Cleanup, then load configuration through the real pipeline.
//
// The environment is set with t.Setenv rather than only through an injected Getenv, for two
// reasons: the §17 test asserts QOMPACK_PROJECT_ROOT really is set, and a hook run through
// RunHook's real-binary mode inherits the process environment. t.Setenv also means a test using
// this helper cannot call t.Parallel, which is intentional — process-wide environment state is not
// something concurrent tests can share.
func NewProject(t *testing.T, opts ...ProjectOpt) *Project {
	t.Helper()

	o := &projectOpts{clock: Epoch}
	for _, opt := range opts {
		opt(o)
	}

	base := t.TempDir()
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home} {
		if err := os.MkdirAll(paths.Long(d), dirPerm); err != nil {
			t.Fatalf("testutil: creating %s: %v", d, err)
		}
	}

	p := &Project{
		Root:  root,
		Clock: NewFakeClock(o.clock),
		home:  home,
		env:   map[string]string{},
		seam:  productionSeam(),
	}

	// QOMPACK_PROJECT_ROOT pins the project root without a .git walk; HOME and USERPROFILE keep
	// the user-global layer and paths.Global inside the temp tree on every platform.
	p.setenv(t, "QOMPACK_PROJECT_ROOT", root)
	p.setenv(t, "HOME", home)
	p.setenv(t, "USERPROFILE", home)
	for k, v := range o.env {
		p.setenv(t, k, v)
	}

	if o.git {
		gitInit(t, root)
	}
	if len(o.files) > 0 {
		p.writeFiles(t, o.files)
	}

	if err := paths.EnsureLayout(paths.Of(root)); err != nil {
		t.Fatalf("testutil: paths.EnsureLayout(%s): %v", root, err)
	}
	if o.hasCfg {
		cfgPath := filepath.Join(paths.Of(root).Dot, "config.json")
		if err := paths.WriteAtomic(cfgPath, []byte(o.cfgJSON), filePerm); err != nil {
			t.Fatalf("testutil: writing %s: %v", cfgPath, err)
		}
	}

	log, closer, err := logging.New(paths.Of(root).Logs, logging.Info)
	if err != nil {
		t.Fatalf("testutil: opening the project logger: %v", err)
	}
	t.Cleanup(func() {
		if cerr := closer.Close(); cerr != nil {
			t.Errorf("testutil: closing the project logger: %v", cerr)
		}
	})
	p.Log = log

	cfg, _, warns, err := config.Load(config.Env{
		ProjectRoot: root,
		HomeDir:     home,
		Getenv:      p.Getenv,
	})
	if err != nil {
		t.Fatalf("testutil: config.Load: %v", err)
	}
	for _, w := range warns {
		t.Logf("testutil: configuration warning: key=%q %s (%s)", w.Key, w.Message, w.Location)
	}
	p.Cfg = cfg

	return p
}

// setenv sets k in both the process environment (restored by t.Cleanup) and the Project's own
// overlay, so Getenv and a spawned child binary always agree.
func (p *Project) setenv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v)
	p.env[k] = v
}

// Getenv is the lookup every Project-scoped call passes to paths.Resolve, config.Load and
// cli.Env: the Project's own overlay first, the process environment second. It is a method value
// so callers can hand it straight to an API that takes func(string) string.
func (p *Project) Getenv(k string) string {
	if v, ok := p.env[k]; ok {
		return v
	}
	return os.Getenv(k)
}

// Home returns the user-global layer's root for this project: paths.Global(Home()) is the
// <home>/.qompack of 00-ARCHITECTURE.md §3.3, and it too lives under t.TempDir().
func (p *Project) Home() string { return p.home }

// Store opens the project's content-addressed store with the project's own configuration, logger
// and clock. Until SP-06 lands, every operation on it reports core.ErrNotImplemented — which is
// exactly what a conformance suite's shape block needs in wave 0.
func (p *Project) Store(t *testing.T) store.Store {
	t.Helper()
	s, err := store.Open(p.Root, p.Cfg, store.Deps{
		Log:   p.Log,
		Clock: p.Clock,
	})
	if err != nil {
		t.Fatalf("testutil: store.Open(%s): %v", p.Root, err)
	}
	return s
}

// WithFiles creates additional project source files after construction and returns the Project so
// calls chain. Keys are slash-separated paths relative to the project root.
func (p *Project) WithFiles(t *testing.T, files map[string]string) *Project {
	t.Helper()
	p.writeFiles(t, files)
	return p
}

// writeFiles is the shared body of the WithFiles option and the WithFiles method.
//
// Every path goes through paths.Long before it reaches the filesystem, which is what lets
// WindowsHostileFiles' >260-character fixture be created on Windows at all. Keys are sorted so a
// failure message names the same file on every run.
func (p *Project) writeFiles(t *testing.T, files map[string]string) {
	t.Helper()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		full := filepath.Join(p.Root, filepath.FromSlash(name))
		if err := os.MkdirAll(paths.Long(filepath.Dir(full)), dirPerm); err != nil {
			t.Fatalf("testutil: creating the directory for %s: %v", name, err)
		}
		if err := os.WriteFile(paths.Long(full), []byte(files[name]), filePerm); err != nil {
			t.Fatalf("testutil: writing %s: %v", name, err)
		}
	}
}

// hookSubcommands maps every name RunHook accepts to the argv the binary expects.
//
// Both spellings are accepted because both are load-bearing in different places: a test that is
// reasoning about the HOST contract writes the host's event name ("PostToolUse"), and a test that
// is reasoning about the CLI writes the subcommand ("observe tool"). §7.3 pairs them, and
// SubagentStop deliberately shares Stop's entry point.
var hookSubcommands = map[string][]string{
	"PostToolUse":      {"observe", "tool"},
	"UserPromptSubmit": {"observe", "prompt"},
	"Stop":             {"observe", "stop"},
	"SubagentStop":     {"observe", "stop"},
	"SessionStart":     {"session-start"},
	"PreCompact":       {"checkpoint"},
	"SessionEnd":       {"flush"},

	"observe tool":   {"observe", "tool"},
	"observe prompt": {"observe", "prompt"},
	"observe stop":   {"observe", "stop"},
	"session-start":  {"session-start"},
	"checkpoint":     {"checkpoint"},
	"flush":          {"flush"},
}

// HookNames returns the six hook subcommands of §7.3 in the order the plugin registers them,
// paired with the host event name each one answers. It exists so a test that must exercise "all
// six hooks" cannot silently exercise five.
func HookNames() []string {
	return []string{"PostToolUse", "UserPromptSubmit", "Stop", "SessionStart", "PreCompact", "SessionEnd"}
}

// RunHook runs one hook against this project and returns its parsed response.
//
// name is either a host event name ("PostToolUse") or the subcommand itself ("observe tool").
// The event is marshalled to JSON and fed to the hook on stdin; a zero CWD is filled in with the
// project root, because a hook refuses to create a store under a root that does not exist and a
// payload cwd must therefore be a real native path (on Windows, "C:\…", never a POSIX "/c/…").
//
// The mode is selected by QOMPACK_E2E_BINARY (see E2EBinaryEnv): unset runs cli.Dispatch in
// process with a real pipe for stdin, set spawns that binary. §6.2 mandates both. Either way the
// exit code is asserted to be 0, because §2.3 permits a hook no other outcome, and stdout is
// parsed as a hookio.Output.
func (p *Project) RunHook(t *testing.T, name string, e hookio.Event) hookio.Output {
	t.Helper()

	argv, ok := hookSubcommands[name]
	if !ok {
		t.Fatalf("testutil: %q is not a hook; want one of %v or its subcommand", name, HookNames())
	}
	if e.CWD == "" {
		e.CWD = p.Root
	}
	if !nativePath(e.CWD) {
		t.Fatalf("testutil: hook payload cwd %q is not an absolute native path; a hook refuses to "+
			"create a store under a root that does not exist, so a POSIX-flavoured cwd would make "+
			"this hook a silent no-op instead of a visible failure", e.CWD)
	}
	payload := marshalEvent(t, e)

	var stdout, stderr []byte
	var code int
	if bin := p.Getenv(E2EBinaryEnv); bin != "" {
		stdout, stderr, code = spawn(t, p.Root, bin, argv, payload, p.childEnv())
	} else {
		stdout, stderr, code = p.dispatchInProcess(t, argv, payload)
	}

	if code != cli.ExitOK {
		t.Fatalf("testutil: hook %s exited %d, but §2.3 permits a hook only 0\nstdout:\n%s\nstderr:\n%s",
			name, code, stdout, stderr)
	}

	var out hookio.Output
	if err := json.Unmarshal(stdout, &out); err != nil {
		t.Fatalf("testutil: hook %s wrote stdout that is not a hookio.Output: %v\nstdout:\n%s", name, err, stdout)
	}
	return out
}

// marshalEvent encodes e the way a host writes a hook payload: compact JSON with HTML escaping
// disabled, so a "<" inside a prompt or a tool result reaches the hook literally.
func marshalEvent(t *testing.T, e hookio.Event) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("testutil: marshalling the hook event: %v", err)
	}
	return b
}

// dispatchInProcess runs the hook through cli.Dispatch with a real io.Pipe for stdin.
//
// A pipe, rather than a bytes.Reader, is deliberate: it delivers the payload in short reads the
// way a host's pipe does, which is the shape hookio.ReadEvent's limit+1 read loop actually has to
// survive. Closing the read half after Dispatch returns unblocks the writer goroutine even when
// the hook stopped reading early (a payload over runtime.hotPath.maxPayloadBytes does exactly
// that), so no goroutine outlives the call.
func (p *Project) dispatchInProcess(t *testing.T, argv []string, payload []byte) (stdout, stderr []byte, code int) {
	t.Helper()

	pr, pw := io.Pipe()
	go func() {
		_, werr := pw.Write(payload)
		_ = pw.CloseWithError(werr)
	}()
	defer func() { _ = pr.Close() }()

	var outBuf, errBuf bytes.Buffer
	env := cli.Env{
		Getenv:  p.Getenv,
		Stdin:   pr,
		Clock:   p.Clock,
		HomeDir: p.home,
	}
	code = cli.Dispatch(context.Background(), cli.All(), append([]string{"qompack"}, argv...), env, &outBuf, &errBuf)
	return outBuf.Bytes(), errBuf.Bytes(), code
}

// childEnv renders the process environment plus this Project's overlay as the "K=V" slice a
// spawned binary needs, with the overlay last so it wins over any inherited value.
func (p *Project) childEnv() []string {
	env := os.Environ()
	keys := make([]string, 0, len(p.env))
	for k := range p.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+p.env[k])
	}
	return env
}

// errorReporter is the slice of *testing.T that AssertAppendOnly actually needs.
//
// The assertion is written against this interface, not against *testing.T directly, so that
// TestProject_AssertAppendOnly can run it against a recorder and assert it REPORTS — which is the
// only way to prove an assertion of this shape has teeth without deliberately failing the test
// that exercises it.
type errorReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// AssertAppendOnly performs, against this live project, exactly the §3.3 append-only conformance
// list, and reports every operation that did not fail the way the invariant requires:
//
//  1. a truncating write to checkpoints/0001.json,
//  2. an in-place rewrite of pins/invariants.jsonl,
//  3. paths.WriteAtomic onto sketches/tried.bloom,
//  4. an out-of-order checkpoint sequence write — writing 0001.json a second time.
//
// All four must fail with core.ErrAppendOnly or os.ErrExist. Nothing else counts: an operation
// that fails with a permission error, or with ENOENT because the file was never created, has not
// demonstrated the invariant, so those are reported too.
func (p *Project) AssertAppendOnly(t *testing.T) {
	t.Helper()
	p.assertAppendOnly(t)
}

// assertAppendOnly is AssertAppendOnly's body, taking the narrower reporter interface.
func (p *Project) assertAppendOnly(t errorReporter) {
	t.Helper()

	l := paths.Of(p.Root)
	checkpoint := paths.CheckpointPath(l, core.CheckpointSeq(1))
	invariants := filepath.Join(l.Pins, "invariants.jsonl")
	bloom := filepath.Join(l.Sketches, "tried.bloom")

	// Seeding is part of the assertion, not setup around it: creating 0001.json once through the
	// sanctioned door is the first half of check 4, and pins/invariants.jsonl and
	// sketches/tried.bloom have to exist before "rewrite it in place" and "replace it" mean
	// anything at all.
	if err := p.seam.CreateNew(checkpoint, []byte("{}\n")); err != nil {
		t.Errorf("append-only: the FIRST write of %s must succeed through CreateNew, got: %v", checkpoint, err)
	}
	if err := paths.AppendJSONL(invariants, map[string]string{"op": "add", "text": "seed"}); err != nil {
		t.Errorf("append-only: seeding %s through AppendJSONL must succeed, got: %v", invariants, err)
	}
	if err := paths.ReplaceBloom(l, []byte("seed"), 1); err != nil {
		t.Errorf("append-only: seeding %s through ReplaceBloom must succeed, got: %v", bloom, err)
	}

	// 1. A truncating write to an immutable checkpoint.
	f, err := p.seam.OpenFile(checkpoint, os.O_WRONLY|os.O_TRUNC, filePerm)
	if f != nil {
		_ = f.Close()
	}
	reportAppendOnly(t, err, "a truncating write to checkpoints/0001.json")

	// 2. An in-place rewrite of the pins ledger: neither an append nor an exclusive create.
	f, err = p.seam.OpenFile(invariants, os.O_WRONLY, filePerm)
	if f != nil {
		_ = f.Close()
	}
	reportAppendOnly(t, err, "an in-place rewrite of pins/invariants.jsonl")

	// 3. WriteAtomic onto the tried-bloom, whose only sanctioned replacement door is ReplaceBloom.
	reportAppendOnly(t, p.seam.WriteAtomic(bloom, []byte("replaced"), filePerm),
		"paths.WriteAtomic onto sketches/tried.bloom")

	// 4. The same checkpoint sequence written twice.
	reportAppendOnly(t, p.seam.CreateNew(checkpoint, []byte("{}\n")),
		"a SECOND write of checkpoints/0001.json (out-of-order sequence)")
}

// reportAppendOnly asserts err is the failure the append-only invariant promises for op.
func reportAppendOnly(t errorReporter, err error, op string) {
	t.Helper()
	switch {
	case err == nil:
		t.Errorf("append-only: %s was ALLOWED; §3.3 and §7.4 require it to fail with core.ErrAppendOnly or os.ErrExist", op)
	case errors.Is(err, core.ErrAppendOnly), errors.Is(err, os.ErrExist):
		// The invariant held.
	default:
		t.Errorf("append-only: %s failed with %v; §3.3 and §7.4 require core.ErrAppendOnly or os.ErrExist", op, err)
	}
}

// nativePath reports whether p is a path the host would actually hand a hook on this platform: an
// absolute native path, never the POSIX "/c/…" form a Git-Bash-flavoured shell produces on
// Windows. A hook refuses to create a store under a root that does not exist, so a payload cwd
// that is not native is a silent no-op rather than a visible failure — which is exactly why this
// check is worth spelling out.
func nativePath(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(p, "/") {
		return false
	}
	return true
}
