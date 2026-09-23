package hostperm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/paths"
)

// maxSettingsBytes bounds one settings source. Real settings files are a few kilobytes; a file past
// this bound is refused as unusable (fail closed) rather than read, so a hostile repository cannot
// make every retrieval parse megabytes.
const maxSettingsBytes = 8 << 20

// maxDropIns bounds managed-settings.d. Past it the managed policy is refused as unusable.
const maxDropIns = 256

// maxReadPatterns bounds the compiled Read path patterns one snapshot holds, summed over every
// source. Evaluating a path costs time in proportion to it, and a request evaluates one path per
// recall hit, drop entry and hash origin, so an unbounded list let a committed settings file make
// every retrieval arbitrarily slow (C1.9 review finding 2). Past the bound the policy is refused as
// unusable, which fails closed exactly as a malformed rule does. Real settings hold tens of Read
// rules; the cost at the bound is measured in plans/sdd/V6-closeout/hostperm/report.md.
const maxReadPatterns = 4096

// maxReadSegments bounds the path segments of those patterns, summed. Matching one path costs time
// in proportion to the segments it is matched against, so a few rules of millions of segments each
// would be as slow as millions of rules; this is 16 segments per pattern at maxReadPatterns, where
// a real rule has two to four.
const maxReadSegments = 16 * maxReadPatterns

// maxNesting bounds the walk for `permissions` objects inside the server-managed settings cache,
// whose on-disk shape is not documented.
const maxNesting = 8

// maxGitFileBytes bounds the two tiny git pointer files read to find a worktree's main checkout.
const maxGitFileBytes = 64 << 10

// ManagedSources names where managed policy may live. See DefaultManaged.
type ManagedSources struct {
	// Dirs hold managed-settings.json and a managed-settings.d directory of *.json drop-ins.
	Dirs []string
	// Opaque are files whose presence means a managed policy this package cannot decode (a macOS
	// configuration profile). A present one makes the whole policy unavailable.
	Opaque []string
	// Registry are Windows registry values holding a settings document.
	Registry []RegistryValue
}

// RegistryValue names one registry value holding a settings JSON document.
type RegistryValue struct {
	// Hive is "HKLM" or "HKCU".
	Hive string
	// Key is the subkey under the hive, e.g. `SOFTWARE\Policies\ClaudeCode`.
	Key string
	// Name is the value name, e.g. "Settings".
	Name string
}

// id names the value in diagnostics.
func (r RegistryValue) id() string { return r.Hive + `\` + r.Key + `\` + r.Name }

// policyKey is where Claude Code reads Windows registry policy.
const policyKey = `SOFTWARE\Policies\ClaudeCode`

// DefaultManaged returns the documented managed-policy locations for goos. getenv supplies
// %ProgramFiles% and $USER; nil means os.Getenv.
func DefaultManaged(goos string, getenv func(string) string) ManagedSources {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch goos {
	case "windows":
		dirs := []string{`C:\Program Files\ClaudeCode`}
		if pf := getenv("ProgramFiles"); pf != "" {
			if d := filepath.Join(pf, "ClaudeCode"); !strings.EqualFold(d, dirs[0]) {
				dirs = append(dirs, d)
			}
		}
		return ManagedSources{Dirs: dirs, Registry: []RegistryValue{
			{Hive: "HKLM", Key: policyKey, Name: "Settings"},
			{Hive: "HKCU", Key: policyKey, Name: "Settings"},
		}}
	case "darwin":
		const prefs = "/Library/Managed Preferences"
		const plist = "com.anthropic.claudecode.plist"
		opaque := []string{prefs + "/" + plist}
		if u := getenv("USER"); u != "" && !strings.ContainsAny(u, `/\`) {
			opaque = append(opaque, prefs+"/"+u+"/"+plist)
		}
		return ManagedSources{Dirs: []string{"/Library/Application Support/ClaudeCode"}, Opaque: opaque}
	default:
		return ManagedSources{Dirs: []string{"/etc/claude-code"}}
	}
}

// source is one place settings may come from.
type source struct {
	// id is the file path or registry value name, for diagnostics.
	id string
	// file is the path to read; empty for a registry value.
	file string
	reg  *RegistryValue
	// opaque marks a policy this package cannot decode.
	opaque bool
	// nested marks the server-managed cache, whose permissions may sit below the top level.
	nested bool
	// settingsDirs are the directories a `/path` rule from this source is measured from.
	settingsDirs []string
}

// sources enumerates every settings source, in the host's own precedence order (managed first).
// The order does not change any answer — rules from every source are unioned — but it keeps
// diagnostics stable.
func (p *Policy) sources() ([]source, error) {
	root := p.o.ProjectRoot
	if root == "" {
		return nil, fmt.Errorf("%w: no project root", ErrUnavailable)
	}
	home := p.home()
	if home == "" {
		return nil, fmt.Errorf("%w: the home directory is unknown, so user settings and ~/ rules cannot be read", ErrUnavailable)
	}
	cfgDir := p.configDir(home)
	managed := DefaultManaged(p.goos, p.o.Getenv)
	if p.o.Managed != nil {
		managed = *p.o.Managed
	}

	var out []source
	for i := range managed.Registry {
		r := managed.Registry[i]
		out = append(out, source{id: r.id(), reg: &r, settingsDirs: []string{root}})
	}
	for _, d := range managed.Dirs {
		mdirs := []string{root, d}
		out = append(out, source{
			id:   filepath.Join(d, "managed-settings.json"),
			file: filepath.Join(d, "managed-settings.json"), settingsDirs: mdirs,
		})
		dropIns, err := listDropIns(filepath.Join(d, "managed-settings.d"))
		if err != nil {
			return nil, err
		}
		for _, f := range dropIns {
			out = append(out, source{id: f, file: f, settingsDirs: mdirs})
		}
	}
	for _, f := range managed.Opaque {
		out = append(out, source{id: f, file: f, opaque: true})
	}
	remote := filepath.Join(cfgDir, "remote-settings.json")
	out = append(out,
		source{id: remote, file: remote, nested: true, settingsDirs: []string{root, cfgDir}},
		source{
			id: filepath.Join(cfgDir, "settings.json"), file: filepath.Join(cfgDir, "settings.json"),
			settingsDirs: []string{cfgDir},
		},
		source{
			id:   filepath.Join(root, ".claude", "settings.json"),
			file: filepath.Join(root, ".claude", "settings.json"), settingsDirs: []string{root},
		},
	)
	for _, dir := range localSettingsDirs(root) {
		f := filepath.Join(dir, ".claude", "settings.local.json")
		out = append(out, source{id: f, file: f, settingsDirs: []string{root}})
	}
	return out, nil
}

// configDir is $CLAUDE_CONFIG_DIR when set, and ~/.claude otherwise.
func (p *Policy) configDir(home string) string {
	if d := p.o.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		if d == "~" || strings.HasPrefix(d, "~/") || strings.HasPrefix(d, `~\`) {
			d = filepath.Join(home, d[1:])
		}
		return d
	}
	return filepath.Join(home, ".claude")
}

// localSettingsDirs returns where settings.local.json may live for this project: the project root
// (the file sits beside settings.json on Windows, outside a repository, and for files an older host
// wrote there) and, when the project is a linked git worktree, the main checkout's root, which is
// where a current host keeps it. Reading both can only add rules.
func localSettingsDirs(root string) []string {
	out := []string{root}
	if main := mainCheckout(root); main != "" && !samePath(main, root) {
		out = append(out, main)
	}
	return out
}

// mainCheckout follows a linked worktree's `.git` file to the main checkout's root, or returns ""
// when root is not a linked worktree or the pointers cannot be followed.
func mainCheckout(root string) string {
	b, err := readSmall(filepath.Join(root, ".git"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	cb, err := readSmall(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(cb))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}

// readSmall reads a regular file of at most maxGitFileBytes.
func readSmall(p string) ([]byte, error) {
	fi, err := os.Stat(paths.Long(p))
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxGitFileBytes {
		return nil, fs.ErrInvalid
	}
	return os.ReadFile(paths.Long(p))
}

// samePath compares two directory paths the way this platform's filesystem would.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if paths.DefaultFold() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// listDropIns lists managed-settings.d's *.json files in the order the host merges them:
// alphabetical, hidden files and other extensions ignored.
func listDropIns(dir string) ([]string, error) {
	entries, err := os.ReadDir(paths.Long(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &transientError{source: dir, text: err.Error()}
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	if len(out) > maxDropIns {
		return nil, sourceError(dir, fmt.Sprintf("more than %d drop-in files", maxDropIns))
	}
	sort.Strings(out)
	return out, nil
}

// signature is what a source looks like from the outside: enough to tell that it changed.
type signature struct {
	id      string
	present bool
	size    int64
	mod     int64
	errText string
}

// sameSignatures compares two signature lists entry for entry.
func sameSignatures(a, b []signature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sign stats one source. A source that is absent is simply absent; one that exists but cannot be
// examined is recorded as an error, which build reports as the policy being unavailable.
func (p *Policy) sign(s *source) signature {
	if s.reg != nil {
		return signRegistry(*s.reg)
	}
	fi, err := os.Stat(paths.Long(s.file))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return signature{id: s.id}
	case err != nil:
		return signature{id: s.id, errText: err.Error()}
	case !fi.Mode().IsRegular():
		return signature{id: s.id, errText: "not a regular file"}
	}
	return signature{id: s.id, present: true, size: fi.Size(), mod: fi.ModTime().UnixNano()}
}

// read returns a present source's bytes.
func (p *Policy) read(s *source) ([]byte, error) {
	if s.reg != nil {
		b, err := readRegistry(*s.reg, p.o.Getenv)
		if err != nil {
			return nil, &transientError{source: s.id, text: err.Error()}
		}
		return b, nil
	}
	f, err := paths.OpenShared(s.file)
	if err != nil {
		return nil, &transientError{source: s.id, text: err.Error()}
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	n, err := buf.ReadFrom(io.LimitReader(f, maxSettingsBytes+1))
	if err != nil {
		return nil, &transientError{source: s.id, text: err.Error()}
	}
	if n > maxSettingsBytes {
		return nil, sourceError(s.id, fmt.Sprintf("larger than %d bytes", maxSettingsBytes))
	}
	return buf.Bytes(), nil
}

// utf8BOM is tolerated at the start of a settings file, which Windows editors write.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// parseSettings extracts the Read deny and ask lists from one settings document.
//
// Everything the host would refuse to load is refused here too, and so is a Read entry this package
// cannot interpret: invalid JSON, a top level that is not an object, a `permissions` that is not an
// object, a `deny` or `ask` that is not an array of strings. Unknown keys are ignored, as the host
// ignores them.
func parseSettings(data []byte, s *source, goos string, fold bool) ([]ruleList, error) {
	data = bytes.TrimPrefix(data, utf8BOM)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return nil, sourceError(s.id, "not a JSON object")
	}
	var blocks []json.RawMessage
	if s.nested {
		collectPermissions(data, 0, &blocks)
	} else if raw, ok := top["permissions"]; ok {
		blocks = append(blocks, raw)
	}
	var out []ruleList
	for _, raw := range blocks {
		var perms map[string]json.RawMessage
		if err := json.Unmarshal(raw, &perms); err != nil || perms == nil {
			return nil, sourceError(s.id, "permissions is not an object")
		}
		for _, k := range []struct {
			key    string
			effect Effect
		}{{"deny", Deny}, {"ask", Ask}} {
			raw, ok := perms[k.key]
			if !ok {
				continue
			}
			l, err := parseList(raw, k.effect, s, goos, fold)
			if err != nil {
				return nil, err
			}
			if l.toolRule != "" || len(l.patterns) > 0 {
				out = append(out, l)
			}
		}
	}
	return out, nil
}

// parseList compiles one deny or ask array.
func parseList(raw json.RawMessage, effect Effect, s *source, goos string, fold bool) (ruleList, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return ruleList{}, sourceError(s.id, "permissions."+effect.String()+" is not an array")
	}
	l := ruleList{effect: effect, source: s.id, settingsDirs: s.settingsDirs}
	for _, e := range entries {
		var entry string
		if err := json.Unmarshal(e, &entry); err != nil {
			return ruleList{}, sourceError(s.id, "permissions."+effect.String()+" holds a non-string entry")
		}
		r, err := parseRule(entry, goos, fold)
		if err != nil {
			return ruleList{}, sourceError(s.id, err.Error())
		}
		switch {
		case !r.relevant:
		case r.toolLevel:
			if l.toolRule == "" {
				l.toolRule = entry
			}
		default:
			l.patterns = append(l.patterns, r.patterns...)
		}
	}
	return l, nil
}

// collectPermissions gathers every object-valued `permissions` key at any depth up to maxNesting.
// It serves the server-managed cache, whose envelope is not documented: wherever its rules sit,
// they apply. Keys are visited in sorted order so diagnostics are stable.
func collectPermissions(raw json.RawMessage, depth int, out *[]json.RawMessage) {
	if depth > maxNesting {
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil && obj != nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := obj[k]
			var probe map[string]json.RawMessage
			if k == "permissions" && json.Unmarshal(v, &probe) == nil && probe != nil {
				*out = append(*out, v)
				continue
			}
			collectPermissions(v, depth+1, out)
		}
		return
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		for _, v := range arr {
			collectPermissions(v, depth+1, out)
		}
	}
}
