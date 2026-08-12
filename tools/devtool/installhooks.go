package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// commitMsgHookScript is the exact commit-msg hook body from the implementation spec §20.
const commitMsgHookScript = "#!/bin/sh\nexec go run ./tools/devtool check-commit-msg \"$1\"\n"

// taskInstallHooks writes .git/hooks/commit-msg (mode 0755).
func taskInstallHooks(args []string) error {
	gd, err := gitDir(root)
	if err != nil {
		return fmt.Errorf("install-hooks: %w", err)
	}
	hooksDir := filepath.Join(gd, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("install-hooks: %w", err)
	}
	p := filepath.Join(hooksDir, "commit-msg")
	if err := os.WriteFile(p, []byte(commitMsgHookScript), 0o755); err != nil {
		return fmt.Errorf("install-hooks: writing %s: %w", p, err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		return fmt.Errorf("install-hooks: chmod %s: %w", p, err)
	}
	fmt.Println("install-hooks: wrote " + p)
	return nil
}

// gitDir resolves the real git directory for repoRoot, handling both the common case
// (repoRoot/.git is a directory) and a worktree checkout (repoRoot/.git is a file containing
// "gitdir: <path>") — §9 explicitly encourages worktrees, so this is not a hypothetical case.
func gitDir(repoRoot string) (string, error) {
	p := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("no .git at %s: %w", repoRoot, err)
	}
	if info.IsDir() {
		return p, nil
	}

	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", p, err)
	}
	line := strings.TrimSpace(string(b))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("unrecognized .git file at %s", p)
	}
	gd := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(repoRoot, gd)
	}
	return filepath.Clean(gd), nil
}
