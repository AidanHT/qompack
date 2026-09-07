# git index fixtures (SP-10, §11 / G2.5)

Small real `.git/index` files, 3 entries each, exercised by `internal/checkpoint/gitindex_test.go`
and seeded into `FuzzParseGitIndex`. They were generated ONCE by the commands below (git
2.47.0.windows.2, in a throwaway temp directory — never in this repository) and are committed as
frozen bytes; tests parse them, they are never regenerated.

```sh
git init -q fixture && cd fixture
git config user.email fixtures@qompack.invalid
git config user.name "SP-10 fixtures"
git config core.autocrlf false
printf 'alpha\n'                 > a.txt          # 6 bytes
mkdir -p b
printf 'beta content\n'          > b/c.txt        # 13 bytes
printf 'gamma content, longer\n' > d.txt          # 22 bytes
git update-index --index-version 2
git add a.txt b/c.txt d.txt
cp .git/index ../v2.index                         # header DIRC, version 2, 3 entries
git update-index --skip-worktree b/c.txt          # sets an extended flag -> index version 3
cp .git/index ../v3.index                         # version 3; b/c.txt has flags&0x4000 set
git update-index --no-skip-worktree b/c.txt
git update-index --index-version 4
cp .git/index ../v4.index                         # version 4 (path-prefix compression): unsupported
head -c 100 ../v2.index > ../truncated.index      # valid header claiming 3 entries, body cut short
```

What each file pins:

| File | Bytes | Purpose |
|---|---|---|
| `v2.index` | 248 | happy-path version-2 parse: paths `a.txt`, `b/c.txt`, `d.txt`, sizes 6/13/22 |
| `v3.index` | 248 | version 3 with one extended-flag entry (`b/c.txt`, skip-worktree) |
| `v4.index` | 241 | version 4 → `errIndexUnsupported` (never a parse attempt) |
| `truncated.index` | 100 | entry count overruns the buffer → `errIndexUnsupported`, no panic |

The mtime/ctime words inside the entries are whatever the throwaway checkout had at generation
time; tests that need a matching working-tree file parse the fixture first and `os.Chtimes` the
file to the recorded mtime rather than assuming any particular instant.
