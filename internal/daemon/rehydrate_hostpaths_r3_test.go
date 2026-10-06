package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// TestRehydrateHostPaths_EscapedAndCaseFoldedSpellingsAreWithheldUnderTheUAT12Rules is the w19c
// round-3 review's leaks through the real host rules and the store's own previews: PowerShell's
// `$Env:` drive (any case but lower was not read), bash's ANSI-C quoting before a denied name, a
// backslash before a quote that cmd.exe and PowerShell keep as a separator, a twice
// percent-encoded separator, a drive-less path with a glob in it (as a command and as a Glob
// preview), and cmd.exe's caret-escaped separators. Each is withheld; the everyday spellings beside
// them, a nested shell that changes to the project root among them, are shown.
func TestRehydrateHostPaths_EscapedAndCaseFoldedSpellingsAreWithheldUnderTheUAT12Rules(t *testing.T) {
	root := uat12Project(t, "proj")
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	// Criterion change (D63): a `$'…'` quote, a `$Env:` variable and a backslash-led or bracket regular
	// expression are all over-withheld (`$`, `\`, `[` are unsafe); a nested shell that changes to the
	// project root is still shown.
	res := requireToolSummaries(t, root,
		[]string{
			bash(`bash -c "cd ` + filepath.ToSlash(root) + ` && go test ./..."`),
		},
		[]string{
			bash(`echo $'done'`),
			bash(`echo $Env:PATH`),
			storePreview(t, map[string]string{"pattern": `\bConfigLoader\b`}),
			storePreview(t, map[string]string{"pattern": `\[DEBUG\]`}),
			bash(`Get-Content $Env:USERPROFILE\.ssh\id_rsa`),
			bash(`cat private/$'deny.txt'`),
			bash(`cat $'.env'`),
			bash(`type "private\"deny.txt`),
			bash(`curl http://h/x?f=private%252Fdeny.txt`),
			bash(`dir \Users\someone\.ssh\*`),
			storePreview(t, map[string]string{"pattern": `\Users\someone\.ssh\*`}),
			bash(`type ^\Users^\someone^\.ssh^\id_rsa`),
		})
	for _, leak := range []string{"id_rsa", "deny.txt", ".env", ".ssh"} {
		require.NotContains(t, res.Text, leak)
	}
}

// TestRehydrateHostPaths_ARuleOverTheProjectThroughALinkWithholdsEveryFreeText is the w19c round-3
// review's linked-root finding through the real host rules: the project is opened through a link
// (a junction on an unprivileged Windows host) and a project rule refuses everything below the
// link's target, spelled from the filesystem root. The host resolves the link and refuses every
// project path, but the screen matched the rule against the root as the request spells it, found it
// covered nothing, and showed free text naming project files by their relative paths. The build
// asks the host about one fresh name below the root, which it refuses, and withholds every free text.
func TestRehydrateHostPaths_ARuleOverTheProjectThroughALinkWithholdsEveryFreeText(t *testing.T) {
	base := shortProjectDir(t)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(base, "real", "work")), 0o700))
	real, err := filepath.EvalSymlinks(filepath.Join(base, "real", "work"))
	require.NoError(t, err)
	root := filepath.Join(base, "proj")
	require.NoError(t, makeDirLink(root, real))
	posix := filepath.ToSlash(real)
	if vol := filepath.VolumeName(real); vol != "" {
		posix = "/" + strings.ToLower(vol[:1]) + posix[len(vol):]
	}
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(/`+posix+`/**)"]}}`)
	writeProjectFile(t, root, "src/main.go")
	refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
	require.NotNil(t, refuses)
	require.True(t, refuses("src/main.go"), "fixture: the host resolves the link and refuses every project path")

	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	res := requireToolSummaries(t, root, nil,
		[]string{bash("cat src/main.go"), bash("go vet ./src/main.go"), bash("git status")})
	require.NotContains(t, res.Text, "main.go")
}
