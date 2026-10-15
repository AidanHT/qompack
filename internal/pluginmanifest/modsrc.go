package pluginmanifest

import (
	"embed"
	"fmt"
	"io/fs"
)

// modSourceFS holds the mod's TypeScript sources, embedded from modsrc/. The pattern takes the
// top-level files only, so the tests under modsrc/tests stay in the source tree and are never
// shipped; the materialising tasks copy them in when they run them.
//
//go:embed modsrc/*.ts
var modSourceFS embed.FS

// ModSources returns the mod's sources for goos, keyed by base name: every top-level .ts file under
// modsrc/ as embedded, except ModTargetFile, which is goos's own ModTargetSource rendering.
//
// Nothing outside modsrc/ is read, and the plugin/ tree this package generates does not change:
// whether and where a bundle carries these sources is the bundle generator's decision.
func ModSources(goos string) (map[string][]byte, error) {
	entries, err := fs.ReadDir(modSourceFS, "modsrc")
	if err != nil {
		return nil, fmt.Errorf("pluginmanifest: read embedded mod sources: %w", err)
	}
	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, readErr := modSourceFS.ReadFile("modsrc/" + e.Name())
		if readErr != nil {
			return nil, fmt.Errorf("pluginmanifest: read embedded %s: %w", e.Name(), readErr)
		}
		out[e.Name()] = b
	}
	if _, ok := out[ModTargetFile]; !ok {
		return nil, fmt.Errorf("pluginmanifest: embedded mod sources lack %s", ModTargetFile)
	}
	out[ModTargetFile] = ModTargetSource(goos)
	return out, nil
}
