package hostperm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// fixedClock is a core.Clock whose time a test sets. It is local because hostperm may not import
// testutil (testutil is a composition root).
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// diskEnv is a hermetic machine on the real filesystem: a project, a home and a managed directory,
// all under t.TempDir, and an environment the test controls.
type diskEnv struct {
	root, home, managed string
	vars                map[string]string
	clock               *fixedClock
	pol                 *Policy
	writes              int
}

func newDiskEnv(t *testing.T) *diskEnv {
	t.Helper()
	e := &diskEnv{
		root: t.TempDir(), home: t.TempDir(), managed: t.TempDir(),
		vars:  map[string]string{},
		clock: &fixedClock{t: time.Now().Add(time.Hour)},
	}
	e.pol = New(Options{
		ProjectRoot: e.root, Home: e.home,
		Getenv:  func(k string) string { return e.vars[k] },
		Managed: &ManagedSources{Dirs: []string{e.managed}},
		Clock:   e.clock,
	})
	return e
}

// write creates a file (and its directories) and stamps it well before the fixed clock, so it is
// outside the racy window unless a test says otherwise. Each write gets a later stamp than the one
// before, as a real edit would.
func (e *diskEnv) write(t *testing.T, file, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(file)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(file), []byte(body), 0o600))
	e.writes++
	stamp := e.clock.Now().Add(-time.Hour).Add(time.Duration(e.writes) * time.Second)
	require.NoError(t, os.Chtimes(paths.Long(file), stamp, stamp))
}

// check evaluates a project-relative path.
func (e *diskEnv) check(t *testing.T, rel string) Effect {
	t.Helper()
	d, err := e.pol.Check(filepath.Join(e.root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return d.Effect
}

func (e *diskEnv) project() string { return filepath.Join(e.root, ".claude", "settings.json") }
func (e *diskEnv) local() string   { return filepath.Join(e.root, ".claude", "settings.local.json") }
func (e *diskEnv) user() string    { return filepath.Join(e.home, ".claude", "settings.json") }

func TestEverySettingsSourceIsRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		file func(e *diskEnv) string
	}{
		{"managed-settings.json", func(e *diskEnv) string { return filepath.Join(e.managed, "managed-settings.json") }},
		{"managed-settings.d drop-in", func(e *diskEnv) string {
			return filepath.Join(e.managed, "managed-settings.d", "20-security.json")
		}},
		{"server-managed cache", func(e *diskEnv) string { return filepath.Join(e.home, ".claude", "remote-settings.json") }},
		{"user", (*diskEnv).user},
		{"project", (*diskEnv).project},
		{"local", (*diskEnv).local},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDiskEnv(t)
			require.Equal(t, Allow, e.check(t, "config/secret.env"))
			e.write(t, tc.file(e), `{"permissions":{"deny":["Read(//**/config/secret.env)"]}}`)
			require.Equal(t, Deny, e.check(t, "config/secret.env"))
			require.Equal(t, Allow, e.check(t, "config/public.env"))
		})
	}
}

func TestDropInsFollowTheHostsFilter(t *testing.T) {
	e := newDiskEnv(t)
	d := filepath.Join(e.managed, "managed-settings.d")
	e.write(t, filepath.Join(d, ".hidden.json"), `{"permissions":{"deny":["Read"]}}`)
	e.write(t, filepath.Join(d, "notes.txt"), `not json at all`)
	require.Equal(t, Allow, e.check(t, "a.txt"), "hidden files and non-.json files are not policy")
}

func TestServerManagedCacheIsReadWhereverItsRulesSit(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, filepath.Join(e.home, ".claude", "remote-settings.json"),
		`{"etag":"x","settings":{"permissions":{"ask":["Read(./notes/**)"]}}}`)
	require.Equal(t, Ask, e.check(t, "notes/a.md"))
}

func TestConfigDirRedirectsUserSettings(t *testing.T) {
	e := newDiskEnv(t)
	alt := t.TempDir()
	e.write(t, e.user(), `{"permissions":{"deny":["Read(./default.txt)"]}}`)
	e.write(t, filepath.Join(alt, "settings.json"), `{"permissions":{"deny":["Read(./relocated.txt)"]}}`)
	require.Equal(t, Deny, e.check(t, "default.txt"))

	e.vars["CLAUDE_CONFIG_DIR"] = alt
	require.Equal(t, Deny, e.check(t, "relocated.txt"))
	require.Equal(t, Allow, e.check(t, "default.txt"), "the host reads only the relocated file")
}

func TestUserSettingsSlashAnchorIsTheConfigDir(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.user(), `{"permissions":{"deny":["Read(/secrets/**)"]}}`)
	require.Equal(t, Allow, e.check(t, "secrets/x"))
	d, err := e.pol.Check(filepath.Join(e.home, ".claude", "secrets", "x"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect)
}

func TestManagedSlashAnchorCoversProjectAndItsOwnDirectory(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, filepath.Join(e.managed, "managed-settings.json"), `{"permissions":{"deny":["Read(/vault/**)"]}}`)
	require.Equal(t, Deny, e.check(t, "vault/a"))
	d, err := e.pol.Check(filepath.Join(e.managed, "vault", "a"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect)
}

func TestWorktreeReadsTheMainCheckoutsLocalSettings(t *testing.T) {
	e := newDiskEnv(t)
	main := t.TempDir()
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	e.write(t, filepath.Join(gitdir, "commondir"), "../..\n")
	e.write(t, filepath.Join(e.root, ".git"), "gitdir: "+gitdir+"\n")
	e.write(t, filepath.Join(main, ".claude", "settings.local.json"), `{"permissions":{"deny":["Read(./x.key)"]}}`)
	require.Equal(t, Deny, e.check(t, "x.key"))
}

func TestUnusableSourcesFailClosed(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, e *diskEnv){
		"invalid JSON in project settings": func(t *testing.T, e *diskEnv) { e.write(t, e.project(), `{"permissions":`) },
		"malformed Read rule in user settings": func(t *testing.T, e *diskEnv) {
			e.write(t, e.user(), `{"permissions":{"deny":["Read(./x"]}}`)
		},
		"a directory where local settings belong": func(t *testing.T, e *diskEnv) {
			require.NoError(t, os.MkdirAll(paths.Long(e.local()), 0o700))
		},
		"oversized managed file": func(t *testing.T, e *diskEnv) {
			e.write(t, filepath.Join(e.managed, "managed-settings.json"),
				`{"x":"`+strings.Repeat("a", maxSettingsBytes)+`"}`)
		},
		"an undecodable managed profile": func(t *testing.T, e *diskEnv) {
			plist := filepath.Join(t.TempDir(), "com.anthropic.claudecode.plist")
			e.write(t, plist, "bplist00")
			e.pol.o.Managed.Opaque = []string{plist}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newDiskEnv(t)
			setup(t, e)
			_, err := e.pol.Check(filepath.Join(e.root, "a.txt"))
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
			_, err = e.pol.Snapshot()
			require.True(t, errors.Is(err, ErrUnavailable), "a second request must fail the same way: %v", err)
		})
	}
}

func TestAMissingHomeOrRootFailsClosed(t *testing.T) {
	_, err := New(Options{}).Snapshot()
	require.True(t, errors.Is(err, ErrUnavailable))
	_, err = New(Options{
		ProjectRoot: t.TempDir(), Home: "", Getenv: func(string) string { return "" },
		Managed: &ManagedSources{},
	}).Check("x")
	if _, herr := os.UserHomeDir(); herr != nil {
		require.True(t, errors.Is(err, ErrUnavailable))
	} else {
		require.NoError(t, err, "with a real home directory the zero Home falls back to it")
	}
	_, err = newDiskEnv(t).pol.Check("")
	require.Error(t, err)
}

func TestUnreadableSettingsFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("platform: a mode bit cannot make a file unreadable to its owner on Windows; " +
			"the directory case in TestUnusableSourcesFailClosed covers the unreadable shape there")
	}
	if os.Geteuid() == 0 {
		t.Skip("platform: root reads a 0000 file regardless of its mode, so this fixture would measure nothing")
	}
	e := newDiskEnv(t)
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./x)"]}}`)
	require.NoError(t, os.Chmod(e.project(), 0))
	t.Cleanup(func() { _ = os.Chmod(e.project(), 0o600) })
	_, err := e.pol.Snapshot()
	require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
}

func TestAReadFailureIsNotCached(t *testing.T) {
	e := newDiskEnv(t)
	require.NoError(t, os.MkdirAll(paths.Long(e.local()), 0o700))
	_, err := e.pol.Snapshot()
	require.Error(t, err)
	require.NoError(t, os.Remove(paths.Long(e.local())))
	_, err = e.pol.Snapshot()
	require.NoError(t, err, "the policy recovers as soon as the source is usable again")
}

func TestSnapshotReloadsOnChangeAndOnlyOnChange(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./a.txt)"]}}`)
	require.Equal(t, Deny, e.check(t, "a.txt"))
	require.Equal(t, Deny, e.check(t, "a.txt"))
	require.Equal(t, 1, e.pol.loads, "an unchanged source is not re-read")

	e.write(t, e.project(), `{"permissions":{"deny":["Read(./b.txt)"]}}`)
	require.Equal(t, Allow, e.check(t, "a.txt"), "an edit is seen by the next request")
	require.Equal(t, Deny, e.check(t, "b.txt"))
	require.Equal(t, 2, e.pol.loads)

	require.NoError(t, os.Remove(paths.Long(e.project())))
	require.Equal(t, Allow, e.check(t, "b.txt"), "a removed source stops applying")
}

func TestARacilyFreshFileIsReRead(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./a.txt)"]}}`)
	now := e.clock.Now()
	require.NoError(t, os.Chtimes(paths.Long(e.project()), now, now))
	require.Equal(t, Deny, e.check(t, "a.txt"))
	// Same size, same timestamp: only the racy rule can see this edit.
	require.NoError(t, os.WriteFile(paths.Long(e.project()), []byte(`{"permissions":{"deny":["Read(./c.txt)"]}}`), 0o600))
	require.NoError(t, os.Chtimes(paths.Long(e.project()), now, now))
	require.Equal(t, Allow, e.check(t, "a.txt"))
	require.Equal(t, Deny, e.check(t, "c.txt"))
}

func TestAParseFailureIsCachedUntilTheSourceChanges(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.project(), `{`)
	_, err := e.pol.Snapshot()
	require.Error(t, err)
	_, err = e.pol.Snapshot()
	require.Error(t, err)
	require.Equal(t, 1, e.pol.loads, "a broken file is parsed once per version, not once per request")
	e.write(t, e.project(), `{"permissions":{}}`)
	_, err = e.pol.Snapshot()
	require.NoError(t, err)
}

func TestSymlinkTargetsAreChecked(t *testing.T) {
	e := newDiskEnv(t)
	target := filepath.Join(e.root, "secret.env")
	e.write(t, target, "S=1\n")
	link := filepath.Join(e.root, "innocent.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("platform: this host will not create a file symlink (" + runtime.GOOS + "): " + err.Error())
	}
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./secret.env)"]}}`)
	require.Equal(t, Deny, e.check(t, "innocent.txt"), "a link to a denied file is itself denied")

	e.write(t, e.project(), `{"permissions":{"deny":["Read(./innocent.txt)"]}}`)
	require.Equal(t, Deny, e.check(t, "innocent.txt"), "the link's own spelling is checked too")
}

func TestJunctionTargetsAreChecked(t *testing.T) {
	e := newDiskEnv(t)
	outside := t.TempDir()
	link := filepath.Join(e.root, "vendor")
	if err := makeDirLink(link, outside); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction (" +
			runtime.GOOS + "): " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(paths.Long(link)) })
	rule := "Read(//" + strings.Join(posixSegments(outside, runtime.GOOS, false), "/") + "/**)"
	e.write(t, e.project(), `{"permissions":{"deny":[`+jsonString(rule)+`]}}`)
	require.Equal(t, Deny, e.check(t, "vendor/key.pem"), "a path through a link is checked where it lands")
	require.Equal(t, Allow, e.check(t, "src/key.pem"))
}

func TestARuleWrittenThroughALinkedDirectoryAppliesAtItsRealLocation(t *testing.T) {
	e := newDiskEnv(t)
	real := filepath.Join(e.home, "real-notes")
	require.NoError(t, os.MkdirAll(paths.Long(real), 0o700))
	if err := makeDirLink(filepath.Join(e.home, "notes"), real); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction (" +
			runtime.GOOS + "): " + err.Error())
	}
	e.write(t, e.user(), `{"permissions":{"deny":["Read(~/notes/**)"]}}`)
	d, err := e.pol.Check(filepath.Join(real, "diary.md"))
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect)
	require.Equal(t, filepath.Join(e.home, ".claude", "settings.json"), d.Source)
}

func TestDefaultManagedLocations(t *testing.T) {
	env := map[string]string{"ProgramFiles": `D:\Apps`, "USER": "alice"}
	get := func(k string) string { return env[k] }
	w := DefaultManaged("windows", get)
	require.Equal(t, []string{`C:\Program Files\ClaudeCode`, filepath.Join(`D:\Apps`, "ClaudeCode")}, w.Dirs)
	require.Len(t, w.Registry, 2)
	require.Equal(t, `HKLM\SOFTWARE\Policies\ClaudeCode\Settings`, w.Registry[0].id())
	m := DefaultManaged("darwin", get)
	require.Equal(t, []string{"/Library/Application Support/ClaudeCode"}, m.Dirs)
	require.Contains(t, m.Opaque, "/Library/Managed Preferences/alice/com.anthropic.claudecode.plist")
	require.Equal(t, []string{"/etc/claude-code"}, DefaultManaged("linux", nil).Dirs)
}

// jsonString quotes s for embedding in a JSON document.
func jsonString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func TestUnavailableErrorsNameTheirSource(t *testing.T) {
	for _, err := range []error{
		&transientError{source: "/x/settings.json", text: "sharing violation"},
		sourceError("/x/settings.json", "not a JSON object"),
	} {
		require.True(t, errors.Is(err, ErrUnavailable))
		require.Contains(t, err.Error(), "/x/settings.json")
		require.Contains(t, err.Error(), ErrUnavailable.Error())
	}
}

func TestConfigDirExpandsATilde(t *testing.T) {
	e := newDiskEnv(t)
	e.vars["CLAUDE_CONFIG_DIR"] = "~/.claude-work"
	require.Equal(t, filepath.Join(e.home, ".claude-work"), e.pol.configDir(e.home))
	e.write(t, filepath.Join(e.home, ".claude-work", "settings.json"), `{"permissions":{"deny":["Read(./w.txt)"]}}`)
	require.Equal(t, Deny, e.check(t, "w.txt"))
}

func TestTooManyDropInsFailClosed(t *testing.T) {
	e := newDiskEnv(t)
	d := filepath.Join(e.managed, "managed-settings.d")
	require.NoError(t, os.MkdirAll(paths.Long(d), 0o700))
	for i := 0; i <= maxDropIns; i++ {
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(d, fmt.Sprintf("%04d.json", i))), []byte(`{}`), 0o600))
	}
	_, err := e.pol.Snapshot()
	require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
}

func TestMainCheckoutIgnoresWhatIsNotALinkedWorktree(t *testing.T) {
	root := t.TempDir()
	require.Empty(t, mainCheckout(root), "no .git at all")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("not a pointer"), 0o600))
	require.Empty(t, mainCheckout(root))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: missing/dir"), 0o600))
	require.Empty(t, mainCheckout(root), "a pointer to nothing")
	gitdir := filepath.Join(root, "g")
	require.NoError(t, os.MkdirAll(gitdir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../elsewhere"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: g"), 0o600))
	require.Empty(t, mainCheckout(root), "a common dir that is not a .git directory")
	require.Equal(t, []string{root}, localSettingsDirs(root))
	require.True(t, samePath(root, root+string(filepath.Separator)))
}

func TestNativePathRoundTrips(t *testing.T) {
	require.Equal(t, `C:\users\u`, nativePath([]string{"c", "users", "u"}, "windows"))
	require.Equal(t, "/etc/x", nativePath([]string{"etc", "x"}, "linux"))
	require.Equal(t, []string{}, ancestorsUp([]string{"a"}, 3))
	_, ok := under([]string{"a"}, []string{"a", "b"})
	require.False(t, ok)
	require.Equal(t, []string{"server", "share", "x"}, posixSegments(`\\?\UNC\server\share\x`, "windows", false))
}
