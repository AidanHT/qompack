// Package hostperm evaluates the host's current file-Read permission rules for one absolute path,
// read-only, so that archived retrieval can refuse content the host would refuse to read today
// (V6-HOST-1, close-out item C1.9).
//
// It reads the same settings files Claude Code reads and applies their `permissions.deny` and
// `permissions.ask` entries that govern the Read tool. It never writes anything, never executes
// anything (a managed `policyHelper` is deliberately not run) and never contacts the network.
//
// # What it reads
//
//   - Managed file policy: managed-settings.json and managed-settings.d/*.json in the system
//     directory (/Library/Application Support/ClaudeCode on macOS, /etc/claude-code on Linux and
//     WSL, C:\Program Files\ClaudeCode on Windows).
//   - Managed registry policy on Windows: the `Settings` value under
//     HKLM\SOFTWARE\Policies\ClaudeCode and HKCU\SOFTWARE\Policies\ClaudeCode.
//   - The cached server-managed settings, <config dir>/remote-settings.json.
//   - User settings, <config dir>/settings.json, where the config dir is $CLAUDE_CONFIG_DIR when
//     set and ~/.claude otherwise.
//   - Project settings, <project>/.claude/settings.json, and local settings,
//     <project>/.claude/settings.local.json (plus the main checkout's copy when the project is a
//     linked git worktree).
//
// A macOS managed configuration profile (/Library/Managed Preferences/…com.anthropic.claudecode
// .plist) is a policy this package cannot decode. Its presence is reported as an unreadable source,
// which the caller must treat as "host policy unavailable".
//
// # How it decides
//
// Rules from every source are unioned: a deny at any level cannot be overridden by an allow at any
// other, so allow rules are never read at all. Deny is checked before ask. Path patterns follow the
// host's gitignore dialect: `//path` is absolute, `~/path` is under the home directory, `/path` is
// relative to the settings source, and `path` or `./path` is relative to the working directory.
// A `!` negation carves out of the working-directory-relative rules listed before it in the same
// list of the same file, and cannot reopen a file inside a directory a rule blocks as a whole.
// Deny rules are checked against both the lexical path and the path its symlinks and junctions
// resolve to, and a rule written through a symlinked directory also applies at its real location.
// On Windows the name the operating system opens is checked as well: a trailing dot or space, a
// `:stream` suffix and an 8.3 short name all open the same file, so a rule on its real name holds
// for each, and a project root or home spelled through short names anchors rules at its real name
// too. A rule written with 8.3 names holds at the real name as well: a short name among its concrete
// directories is expanded, and one after a glob or in a literal rule is compared with each path
// segment's own 8.3 name. Two paths cannot be judged, so under any deny or ask rule they are refused
// as denied: one holding an 8.3-shaped name (a tilde and a digit) that names nothing on disk, whose
// long name is unknown, and, while a rule names an 8.3 name after a glob, one that does not exist,
// whose 8.3 names are unknown. Paths are compared in the host's POSIX form (C:\x becomes /c/x) and case-insensitively on
// Windows and macOS. More than 5000 path patterns, or 80 000 path segments across them, make the
// rules unusable (fail closed), which bounds what one path's check can cost.
//
// Where the documentation leaves a choice open this package takes the one that refuses more, never
// less: a `/path` rule in a managed or cached source is anchored at both the project and the file's
// own directory, separate managed files do not carve out of each other, and a single-segment
// directory pattern such as `secrets/**` matches at any depth.
//
// # What it cannot see
//
// Session-only rules (added with /permissions and not saved), --allowedTools/--disallowedTools and
// --settings flags, --setting-sources exclusions, PreToolUse hooks, an embedding host's SDK
// managedSettings, a managed policyHelper's output and the server-managed policy that has not been
// cached on disk are invisible to a plugin. So is the session's working directory when it differs
// from the project root, and CLAUDE_CONFIG_DIR when the host scrubs it from subprocess environments.
// A caller must therefore never describe a pass here as the host's permission decision; it is the
// part of that decision a plugin can reconstruct, applied fail-closed.
//
// Rule syntax and file locations follow the official documentation as fetched on 2026-09-22:
// https://code.claude.com/docs/en/permissions, https://code.claude.com/docs/en/settings and
// https://code.claude.com/docs/en/managed-settings.
package hostperm
