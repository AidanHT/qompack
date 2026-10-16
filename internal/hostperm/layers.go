package hostperm

import (
	"errors"
	"path/filepath"
)

// LayerKind names which settings layer a Layer is.
type LayerKind string

// The settings layers, in the host's precedence order (managed first), as sources lists them.
const (
	LayerRegistry       LayerKind = "registry"        // a Windows registry policy value
	LayerManaged        LayerKind = "managed"         // managed-settings.json in a managed directory
	LayerManagedDropIn  LayerKind = "managed-drop-in" // a managed-settings.d/*.json drop-in
	LayerManagedProfile LayerKind = "managed-profile" // a macOS configuration profile, never decoded
	LayerRemote         LayerKind = "remote"          // <config dir>/remote-settings.json
	LayerUser           LayerKind = "user"            // <config dir>/settings.json
	LayerProject        LayerKind = "project"         // <project>/.claude/settings.json
	LayerLocal          LayerKind = "local"           // <project>/.claude/settings.local.json
)

// Layer is one settings source as ReadLayers found it.
type Layer struct {
	Kind LayerKind
	// Source is the file path or registry value name.
	Source string
	// Present reports that the source exists.
	Present bool
	// Opaque marks a present source in a format this package cannot decode; it is not read.
	Opaque bool
	// Data is a present, readable source's bytes exactly as stored (a UTF-8 byte-order mark
	// included). It is nil for an absent, opaque or unreadable source.
	Data []byte
	// Err is why a source that exists, or may exist, could not be read.
	Err error
}

// errHomeUnknown is the Err of the user and remote layers when no home directory is known and
// CLAUDE_CONFIG_DIR does not name an absolute directory.
var errHomeUnknown = errors.New("the home directory is unknown, so the config directory cannot be found")

// ReadLayers lists every settings layer the permission policy reads and returns each present one's
// raw bytes. It is read-only and decodes nothing: what a layer's document says is the caller's to
// judge. It never fails as a whole. A source it cannot examine or read carries its own Err, and an
// empty ProjectRoot omits the project and local layers rather than refusing, so a caller with no
// project still sees the managed, registry, remote and user layers.
//
// It reads through the same stat and shared, bounded read the policy uses, and Options means what it
// means for New. With a project root it names exactly the sources a Policy reads, in the same order
// (TestReadLayersNamesThePolicysSources).
func ReadLayers(o Options) []Layer {
	p := New(o)
	var out []Layer
	for _, ls := range p.layerSources() {
		out = append(out, p.readLayer(ls))
	}
	return out
}

// layerSource is one entry of layerSources: a source, its kind, and an enumeration error that
// stands in for reading it.
type layerSource struct {
	kind LayerKind
	src  source
	err  error
}

// layerSources mirrors sources without its two refusals: an empty project root drops the project
// and local layers, and an unknown home marks the user and remote layers unreadable.
func (p *Policy) layerSources() []layerSource {
	root := p.o.ProjectRoot
	managed := DefaultManaged(p.goos, p.o.Getenv)
	if p.o.Managed != nil {
		managed = *p.o.Managed
	}

	var out []layerSource
	for i := range managed.Registry {
		r := managed.Registry[i]
		out = append(out, layerSource{kind: LayerRegistry, src: source{id: r.id(), reg: &r}})
	}
	for _, d := range managed.Dirs {
		f := filepath.Join(d, "managed-settings.json")
		out = append(out, layerSource{kind: LayerManaged, src: source{id: f, file: f}})
		dir := filepath.Join(d, "managed-settings.d")
		dropIns, err := listDropIns(dir)
		if err != nil {
			out = append(out, layerSource{kind: LayerManagedDropIn, src: source{id: dir}, err: err})
			continue
		}
		for _, f := range dropIns {
			out = append(out, layerSource{kind: LayerManagedDropIn, src: source{id: f, file: f}})
		}
	}
	for _, f := range managed.Opaque {
		out = append(out, layerSource{kind: LayerManagedProfile, src: source{id: f, file: f, opaque: true}})
	}

	if cfgDir, ok := p.layerConfigDir(); ok {
		remote := filepath.Join(cfgDir, "remote-settings.json")
		user := filepath.Join(cfgDir, "settings.json")
		out = append(out,
			layerSource{kind: LayerRemote, src: source{id: remote, file: remote, nested: true}},
			layerSource{kind: LayerUser, src: source{id: user, file: user}},
		)
	} else {
		out = append(out,
			layerSource{kind: LayerRemote, src: source{id: "remote-settings.json"}, err: errHomeUnknown},
			layerSource{kind: LayerUser, src: source{id: "settings.json"}, err: errHomeUnknown},
		)
	}

	if root == "" {
		return out
	}
	project := filepath.Join(root, ".claude", "settings.json")
	out = append(out, layerSource{kind: LayerProject, src: source{id: project, file: project}})
	for _, dir := range localSettingsDirs(root) {
		f := filepath.Join(dir, ".claude", "settings.local.json")
		out = append(out, layerSource{kind: LayerLocal, src: source{id: f, file: f}})
	}
	return out
}

// layerConfigDir is configDir when it can be found: an absolute CLAUDE_CONFIG_DIR needs no home.
func (p *Policy) layerConfigDir() (string, bool) {
	if home := p.home(); home != "" {
		return p.configDir(home), true
	}
	if d := p.o.Getenv("CLAUDE_CONFIG_DIR"); d != "" && filepath.IsAbs(d) {
		return d, true
	}
	return "", false
}

// readLayer stats one source and, when it is present and decodable, reads it.
func (p *Policy) readLayer(ls layerSource) Layer {
	l := Layer{Kind: ls.kind, Source: ls.src.id}
	if ls.err != nil {
		l.Err = ls.err
		return l
	}
	sig := p.sign(&ls.src)
	switch {
	case sig.errText != "":
		l.Err = errors.New(sig.errText)
		return l
	case !sig.present:
		return l
	}
	l.Present = true
	if ls.src.opaque {
		l.Opaque = true
		return l
	}
	data, err := p.read(&ls.src)
	if err != nil {
		l.Err = err
		return l
	}
	l.Data = data
	return l
}
