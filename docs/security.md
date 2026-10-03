# Security and recovery

What Qompack protects and what remains unverified. SP-17's historical packaged measurements are
under `plans/sdd/V6-SP-17-packaging-hardening-and-release/`. Original V6 failures remain under
`plans/sdd/V6-VERIFY/`; corrections and new evidence are tracked separately under
`plans/sdd/V6-remediation/`. A source correction alone does not establish release acceptance.

Configuration keys are named here but never described: `docs/config-reference.md` is generated from
the schema and is the only place that documents a default. Tool schemas are in `docs/mcp-tools.md`,
commands in `docs/commands.md`.

## 1. Trust boundaries

Qompack stores what the host has already given the model and hands it back on request. Two
boundaries carry that, and both run in one direction only.

**Required boundary: host permission outranks the archive.** A stored content hash or a host
`tool_use_id` is an address, never a credential. Path-bearing `recall` hits, `expand` by tool-use ID
and `re_read` check current filesystem scope, including directory junctions. Root and chunk hashes
must establish every recorded origin before reading bytes; a permissive duplicate cannot hide a
restricted origin. Missing or incomplete provenance returns unavailable. File captures with a lost
path are refused; known pathless producers remain subject to current redaction.

**The host's saved Read rules are re-checked on every retrieval (V6-HOST-1).** After the
filesystem-scope check, every record that has a path is checked against the `permissions.deny` and
`permissions.ask` rules for `Read` in the settings files Claude Code reads: managed
(`managed-settings.json` and `managed-settings.d/` in the system directory, the Windows policy
registry values, the cached server-managed settings), user (`~/.claude/settings.json`, or
`$CLAUDE_CONFIG_DIR/settings.json`), project (`.claude/settings.json`) and local
(`.claude/settings.local.json`). A file is re-read as soon as it changes, so a rule added mid-session
applies to the next call. The check covers `expand` by tool-use ID, root hash and chunk hash;
`re_read` in every `at` form, including a hash named under a different path; the hits and summaries
of `recall`; the evidence preview of `why`; and pointers into the archive listed by `dropped`.
`timeline` carries no path-bearing content. A deny rule answers `denied` with its own reason; an ask
rule is refused too, with a different reason, because a plugin cannot prompt; and a settings file
that exists but cannot be read or parsed makes every path-bearing answer `unavailable` ("host policy
unavailable") until it is fixed. No refusal echoes the path or the rule. Records with no path
(shell output, prompts) have nothing for a path rule to match and keep the behaviour above.
Every spelling that reaches the served content is judged, and a rule matching any one of them
refuses: the path as asked for or recorded, the real name whose history is served, the name the
operating system opens for each (on Windows a trailing dot or space, a `:stream` suffix or an 8.3
short name opens the same file) and where each resolves through links. A rule written with 8.3
names holds at the file's real name too. Two paths cannot be judged on Windows, so under any deny
or ask rule they answer `denied` with the deny rule's reason, never the ask reason: one holding an
8.3-shaped name (a tilde and a digit, such as a deleted file's `CREDEN~1.SEC`) that names nothing on
disk, because its long name is unknown, and, while a rule names an 8.3 name after a glob (such as
`Read(**/CREDEN~1.SEC)`), a file that no longer exists, because its 8.3 names are unknown. A rule
names an 8.3 name only with a tilde followed by a digit or by a glob character that can stand for
one (`Read(**/CREDEN~?.SEC)`), or with a tilde inside a bracket expression
(`Read(**/CREDEN[~]1.SEC)`); a backup-file rule such as `Read(**/*~)` names none, so deleted files
are judged under it as before. More
than 5000 Read path patterns, or 80 000 path segments across them, in force at once makes the rules
unusable, so path-bearing content is withheld as for an unreadable file; the bound keeps every
check's cost bounded.

**That is the part of the host's decision a plugin can read, not the decision itself.** Rules added
for one session only, `--allowedTools`/`--disallowedTools`/`--settings`/`--setting-sources` flags,
PreToolUse hooks, an embedding host's managed settings, a managed `policyHelper`'s output and the
session's working directory (when it is not the project root) are invisible to a plugin. A macOS
configuration profile cannot be decoded, so its presence makes path-bearing content unavailable.
Where the documentation leaves a reading open, Qompack takes the one that refuses more; see
`internal/hostperm`'s package comment and [docs/cannot-do.md](cannot-do.md#5-trust-boundary).
Redaction of credential patterns does not authorize an otherwise denied archived read. V6 evidence
and the required owner corrections are recorded in `plans/sdd/V6-VERIFY/`,
`plans/sdd/V6-remediation/` and `plans/sdd/V6-closeout/hostperm/`. Checkpoint summaries and drop
reasons receive retrieval-time redaction too; evidence hashes remain metadata, and their contents
require a separate authorized expansion.

**The rehydration block follows the same rules for its pointers.** After a compaction, a file or
tool pointer whose path these rules deny or ask about, or which lies outside the project, points by
content hash instead. A tool call's argument summary that is one path argument (a Read's file, a
Glob's or Grep's lone argument, a path-named JSON argument) is judged as that path; one that holds
more than a path (a list, a line locator such as `#L4`) is screened as free text as well. Any other
summary (a command, a query, a prompt) is free text: it is withheld when it contains the literal
part of a Read deny or ask rule (`secrets` for `Read(./secrets/**)`, `deny.txt` for
`Read(./private/deny.txt)`) or the name of a path the block withholds, once quotes, escapes and
line continuations are removed, or an absolute path outside the project (a home directory or an
environment variable in any shell's spelling included, and a path glued to a flag), or a `recall`
`path:` selector that selects a withheld path; and every free-text summary is withheld while the
rules cannot be read. A checkpoint drop entry keyed by a withheld path stays in `dropped()` with the
path replaced by the pointer's hash or `(path withheld)`, and no drop reason, the checkpointer's own
git or scan errors included, shows such a path or one outside the project. Drop entries that carry
the model's own text (an `already_tried` call, an open question) are not gated, nor are the block's
records of eliminated approaches and decisions, which are the model's own earlier text too. The
screen's limits (names built at run time or relative to an earlier `cd`, and 8.3 aliases, links,
globs and brace expansions typed in a command, are not resolved; a text that merely mentions a
rule's literal is withheld) are in [ADR 0011 §23](adr/0011-rehydration-budget-and-item-order.md).

**A refusal is not an oracle.** The refusal sentence never echoes the offending path, so denials
cannot be used to probe what exists outside the project. Measured across three escape shapes and
three tools: no response echoed an escaping path or an absolute path outside the root.

**The archive is not a second filesystem.** `re_read` answers from recorded history and never falls
back to a live disk read — measured after the captured file was replaced wholesale, and again after
it was deleted. The archived bytes came back both times; the replacement never did.

**Archived text is data with provenance, never instruction.** Everything in the store arrived from a
tool result, which is whatever some file or command printed, which an attacker may control. Every
response carrying stored bytes sets `_meta.qompack.untrusted`, and nothing in the retrieval path can
execute anything: `internal/mcp`'s transitive import set contains no `os/exec` across 155 packages.
A capture whose arguments were a shell command that would create a sentinel file was retrieved
through four tools and the sentinel was never created.

**The daemon's executable on Windows is a verified copy, not the plugin's file (C1.17).** Because
the daemon outlives the session and Windows will not remove a running executable's directory, a
process running the plugin's own binary — inside `CLAUDE_PLUGIN_ROOT`, or laid out as a plugin's,
`bin/qompack.exe` with `.claude-plugin/plugin.json` beside `bin/`, which covers `qompack mcp`'s
lazy spawn — starts it from `<home>/.qompack/bin/<sha256>/qompack.exe` rather than the plugin's
`bin/qompack.exe` (`internal/daemon/spawn_stage.go`). The copy lives under the user's own profile —
the one place outside a project Qompack writes
([architecture §2](architecture.md#2-write-set-and-retention)) — sealed read-only, and it is checked
before every spawn: a regular file, not a link, junction or other reparse point, whose SHA-256 matches both
its directory name and the spawning process's own executable, and still the file filed there once
it has been hashed. A file that fails any of that is removed and replaced, never run — one that
cannot be read at all, whatever the reason, included — and a source that changes while it is being
copied is never filed. The one exception is a copy another program holds open without sharing
read: that proves nothing about its bytes and passes when the program lets go, so it is neither run
nor removed — that one spawn starts the daemon from the plugin binary, and the next spawn checks
the copy again. The boundary is the same one the plugin directory already has: a process running as
the same user could replace either file; another user cannot write either. A window of microseconds
remains between the last check and the process start, which owner decision D38 accepted. On Linux and
macOS nothing is copied and the daemon runs from the plugin binary.

**The store ignores itself in git, and that is all "self-ignore" means.** On first use Qompack writes
`<project>/.qompack/.gitignore` containing `*` (`internal/paths/layout.go`, `EnsureLayout`), so git
ignores everything in the store (captures, checkpoints, logs, backups) even in a project whose own
`.gitignore` never mentions it, and committing the working tree does not commit recorded session
content. It does not remove anything already committed, and a tool that does not read `.gitignore`
files, such as an archiver, a sync client or a backup program, still sees the store. It is not a
capture filter either: Qompack does not leave its own activity out of the record. A tool call that
reads a file under `.qompack/` is captured like any other. While `retrieval.ephemeralResults` is on
(the default), the answer of every Qompack tool except `record_eliminated` is written back as an
ephemeral record of its own, filed under `mcp__qompack__<tool>` with an id that starts
`qompack-mcp:`, and the host's capture of the model's call to a Qompack tool is recorded under
the name the host gave the call, `mcp__plugin_<entry>_qompack__<tool>`. `recall` ranks both kinds of
self-record after the original captures, whatever marketplace entry name the host put in that tool
name (owner decisions D49 and D53(e)).

## 2. Redaction: what it covers, and what it does not

Redaction runs **once, on the way in**, at a single choke point (`internal/redact`: ten built-in
credential families plus any `runtime.redact.patterns` an operator adds). The store is
content-addressed and immutable, so a secret that reaches `objects/` cannot be removed without
breaking every root referencing its chunk — which is why the choke point is on the write path, and
why a nil redactor means the *default* rule set rather than no redaction. Retrieval re-applies the
**current** policy on the way out, so a record captured before a rule existed is not served in the
clear forever; with no redactor wired, retrieval serves nothing and reports itself unavailable
rather than serving unchecked bytes.

**Measured coverage.** A session carrying all ten built-in families plus an operator pattern was
driven through the packaged binary, checkpointed, flushed, backed up and exported, and every file
the product wrote was swept for each credential's literal value and for any surviving run longer
than eight bytes. Clean: `objects/`, `index/` (including the `tool_use.jsonl` args preview),
`records/`, `checkpoints/`, `pins/`, `spool/`, `state/`, `metrics/`, `logs/`, `dag/`, `sketches/`,
`run/`, the `.qompack` root, the backup tree and its manifest, and the seven retrieval responses the
MCP server put on the wire. `logs/LOUD.log` is checked by a case of its own, which arranges a real
Loud through the product's §12.3 quarantine path and then scans the file by name: the Loud carries a
twelve-character hash prefix and a reason, never a span of the content.

**What it does not cover.**

- **The eval exporter is a second redaction path, not the same one.** Its rule set now covers the
  credential shapes `internal/redact` covers — including the long GitHub token and the `ghu`/`ghs`/
  `ghr` prefixes it used to miss — and it applies the operator's own `runtime.redact.patterns`.
  It is still a separate implementation reached by a separate, opt-in command that refuses a
  destination inside the working tree and needs a second environment variable to emit unredacted
  output. Treat an export as a distinct artifact and review one before sharing it.
- **`LOUD.log` is append-only, never rotated, and never backed up.** It grows for the life of the
  project, and a backup deliberately skips `logs/` entirely. It was swept by name and found clean,
  and that is a property of caller discipline rather than of a redaction layer: `internal/logging`
  has none. A future caller that logged a payload would be caught by review and by nothing else.
- **The spool is not redacted by the store.** What lands in `spool/` is whatever the capture path
  already admitted, and the capture path redacts before handing anything over — measured clean. But
  the backup walk copies the spool, so anything a capture retains is copied with it. The protection
  is upstream, not at the copy.
- **Structured file targets must remain inside the project before capture.** The hook client and
  daemon reject out-of-project targets before retaining payload bytes. The observer also checks
  scope before writing objects. This corrects the earlier S-6 dropped-path behavior; it does not
  erase old records or backups. Legacy file records with missing provenance remain unavailable to
  retrieval. Shell command text is not interpreted as a complete file-access policy, so pathless
  shell output still depends on content redaction and remains untrusted.
- **Redaction is not erasure across media.** Nothing here promises secure physical erasure of a
  credential already written to a backup, a copy, or a filesystem with snapshots.

## 3. Decode and size bounds

Every bound is a named constant, and each was exercised against hostile input on the packaged binary
or the real store. None is advisory.

| bound | value | where | what it stops |
| --- | --- | --- | --- |
| `maxDecodedSize` | 64 MiB | `internal/store/compress.go` | a decompression bomb — the decoder is built with this memory limit, so a frame expanding past it fails instead of allocating |
| `encodedObjectLimit()` | zstd worst-case framing over `MaxPutBytes` | `internal/store/compress.go` | an object *file* larger than any legal encoding; the read stats first and refuses before the first byte |
| `store.MaxPutBytes` | 64 MiB | `internal/store/fsstore.go` | an unbounded single put; the input is truncated rather than failing, because every producer is a hook |
| `hookCaptureMaxBytes` | 4 MiB | `internal/cli/capture_admission.go` | a host delivery larger than the process will allocate — applied **before** configuration is read |
| refusal prefix | 4 KiB | `internal/cli/capture_admission.go` | the evidence a refused delivery may carry forward |
| `runtime.mcp.maxResponseBytes` | see `docs/config-reference.md` | `internal/config/defaults.go` | an unbounded retrieval response; `full` means "do not narrow to the matching hunk", never "unbounded" |
| `ipc.MaxLineBytes` | 1 MiB | `internal/ipc/wire.go` | an oversize request line; configuration may tighten it and may never raise it |

Measured: an 80 MiB bomb compressed to a few kilobytes is refused by `store.Decode` before it is
written to disk; a 128 MiB object file is refused on its size alone; a 6 MiB `tool_response` is
refused by the capture cap with only a bounded prefix retained, and the hook still exits 0 with
host-parseable output; and a full `expand` of a 24 636-byte object against a 4096-byte response
bound returned exactly 4096 bytes, marked truncated, with a cursor to page on.

`runtime.hotPath.maxPayloadBytes` is bounded from **both** sides: a value above the hard capture cap
is a violation that is restored to the cap with a warning, rather than one that silently refuses
every delivery. And a configuration problem in one key no longer disables capture wholesale: the
hot-path loader applies the same per-leaf fallback `config print` does — an invalid value falls back
and is recorded in `state/config-violations.json`, and an unknown or mistyped key is dropped with a
warning. Two kinds of problem still refuse every capture: input the hooks cannot read safely — a
config file that does not parse, is not a plain file or is over its size bound — and a setting that
cannot be applied as written of either control over what is recorded: `runtime.redact`, the privacy
policy itself, where a fallback would record under a policy you did not write, and `runtime.mode`,
the capture switch, where a fallback to `auto` would record while you were switching recording off
(an `"OFF"` or a `false` refuses, exactly as `off` would). **Check that file, and `qompack self-test`'s `config.capture` row, after
changing configuration** — they are where the product says which of your values it refused, and
whether the hooks refused all of them
([docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility)).

## 4. Quarantine, retention and what a damaged object answers

An object whose bytes cannot be trusted is never served and never silently dropped. The read path
verifies, in order: the file is a regular file that did not change between stat and open; its
physical size is within the read bound; it decodes; its plaintext length matches the index; and its
content hashes to the address it is filed under. Any failure moves the bytes to
`.qompack/tmp/quarantine/<attempt>/`, Louds with the hash's twelve-character short form and nothing
else, counts `store.quarantined`, and returns not-found.

Measured against five hostile objects — a damaged frame, a truncated frame, an oversize file, a
decompression bomb, and a valid frame of the right length whose plaintext hashes elsewhere — all
five were refused and all five quarantined. Afterwards the daemon still held its lock, the MCP
server still expanded a healthy object, and the `checkpoint` hook still exited 0.

A damaged object reaches the model as the **`unavailable`** domain outcome — a third answer beside
"found" and "not found" — on both address forms (`tool_use_id` and bare chunk hash). It is never a
protocol error and never a miss: telling a model the content was never there, when in fact it was
refused and preserved, is what §12.3 forbids.

**Quarantined evidence survives collection.** A garbage-collection pass with retention disabled on
both axes — the most destructive form the API can express — deleted five objects and left the
quarantined object, a `.corrupt.` sketch, and every `records/captures/` sidecar exactly where they
were. Repairs preserve diagnostic and quarantine evidence; there is no destructive default cleanup.

A read-only pass never quarantines anything. `qompack fsck` without `--repair` opens the store
read-only, so asking about a damaged object does not relocate it.

## 5. What recovers on its own

Each of these was cut on the installed bundle and came back, with no operator.

- **A killed daemon.** Deliveries in flight are spooled; the next session-start brings a new daemon
  up over that spool and replays it. Nothing is lost and nothing is left dangling.
- **A crash during a session.** The next session-start resumes recording into the same project.
- **A read-only `.qompack/objects`, or a spool that cannot be written.** Deliveries are spooled
  rather than dropped; when the restriction lifts, the next daemon drains them.
- **Two daemons for one project.** Exactly one holds the lock; the second exits 0 without touching
  the project. Hooks delivered during the contention exit 0.
- **A lock left behind by a daemon that died.** It is reclaimed once its heartbeat goes stale — up
  to **90 seconds** (`internal/daemon`'s `staleAfter`). During that window recording does not
  resume: hooks still exit 0 and events go to the spool for whichever daemon wins the lock. Said
  plainly because "Qompack stopped recording for a minute and a half after a crash" is otherwise
  indistinguishable from a fault.
- **A torn delivery seal, including a half-present pair.** The journals are untouched and the next
  daemon rebuilds the derived position files.
- **A corrupt `run/state.bin`.** Rewritten from scratch on the next run.
- **An MCP server killed mid-request.** The host sees the session end; the archive is unchanged. A
  retrieval server is a reader, and a killed reader cannot leave the store different.
- **Every malformed lifecycle sequence we could construct** — a duplicate tool call, a duplicate
  PreCompact, a compaction whose summary never arrived, a compaction whose SessionStart landed under
  another session's id, a turn the host never delivered, a PreCompact before the events it would
  summarise, a SessionEnd before its Stop. Hooks exit 0, the index gains no duplicate record, and no
  reference dangles.

## 6. What stays explicitly incomplete

These do **not** heal, and the product says so rather than pretending otherwise (§13 invariant 10).

- **A checkpoint whose artifact is missing, or whose bytes no longer match its MANIFEST digest.**
  The affected checkpoint is refused and its parent used instead; the artifact is never repaired in
  place. An orphan artifact, a MANIFEST entry without its artifact and a digest mismatch each Loud
  once, naming the artifact. `qompack fsck` re-hashes the tier and can append a MANIFEST line for a
  clean orphan; nothing deletes a checkpoint.
- **Spool bytes a drain has not replayed.** Named as a pending gap rather than assumed delivered. A
  torn WAL segment or client spool file leaves the torn record behind and the rest is replayed.
- **A capture whose bytes were never admitted** — denied, unavailable, corrupt. A sidecar under
  `records/captures/` records the outcome and why. Sidecars are evidence and are never swept.
- **A damaged or missing historical object.** It is quarantined on first read and never collected.
  Restoring it needs a backup; there is no reconstruction.

## 7. What needs an operator, and how to find it

Run **`qompack fsck`** (integrity, eighteen check rows, read-only by default) and **`qompack
doctor`** (capability, version, scope and control rows). Between them they name every state below.
`fsck --repair --yes` performs five explicit repairs and no others: quarantine a damaged object,
regenerate `index/files.json`, regenerate `pins/invariants.json`, rebuild `tried.bloom` from the
elimination records, active and stale, and append a MANIFEST line for a clean orphan checkpoint
artifact. Nothing else is
rewritten, and nothing is ever deleted.

1. **A corrupt `.qompack/config.json`.** The daemon will not start; hooks keep exiting 0 and
   recording has stopped. Repair or delete the file and the defaults reload on the next run. The
   failure is Loud and reaches the user-level log even when the project's own log directory is
   unwritable.
2. **A torn index line.** `index/roots.jsonl` and `index/tool_use.jsonl` are append-only logs; a
   line torn by a crash costs that record. Appends are newline-guarded, so a torn tail no longer
   swallows the *next* record as well. `fsck` reports the malformed lines; truncating the file back
   to its last complete newline recovers everything below it.
3. **A stale `state/drain.json`.** A progress document claiming durable bytes the spool no longer
   has wedges the spool: the drain refuses to consume it, and Louds once at startup saying so. New
   deliveries are still recorded; the stranded ones are not replayed until the document is
   corrected. It is *derived* state, so deleting it is the right repair — the journals it summarises
   are not.
4. **A truncated `state/retention-roots.jsonl`.** Collection is not stopped. The unparseable line is
   treated as a claim nobody may ignore, so everything on it is retained under a blanket rollback
   label — the cost is retention of material nothing can account for. It Louds once per pass.
   Truncating back to the last complete line restores the file; the claims the torn line carried are
   gone either way.
5. **A stage-1 capture sidecar** — an `observe.tool` delivery whose sidecar carries a durable
   `bytes_hash` with no reference joined. This is **reportable, never repairable**: the sidecar is
   the evidence that a capture happened, and "fixing" it by inventing a reference, or by deleting
   it, would destroy the only record that it happened at all. `fsck` names it.
6. **An object removed while its index line survives.** A root naming a chunk `objects/` does not
   hold. `fsck`'s object and index classes resolve every reference against disk and name the
   affected records; the only repair is a backup.

## 8. Known limitations

Open at this release, stated here rather than left to discovery.

- **Host Read permission is reconstructed from settings files, not queried.** Archived retrieval
  honours the Read deny and ask rules saved in the files Claude Code reads (§1), but a read the host
  would refuse only through a session-only rule, a command-line flag, a hook or an embedding host's
  policy can still be served from the archive. A path rule never matches a pathless record: the
  output of `cat .env` archived as shell output is served even when `Read(./.env)` is denied. A
  search result is judged by the directory it searched, as the host judges a Grep, so its lines may
  quote a file a later rule denies. `CLAUDE_CONFIG_DIR` is seen only if the host passes it to
  subprocesses (`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` removes it).
- **Whether a live gap is visible depends on whether a daemon is running when you look.** Counters
  that name a degradation live in the daemon's status snapshot, so `qompack status --json` after a
  session ended does not carry them. Ask while the session is live, or ask `qompack fsck`.
- **Fault injection is hook-side only.** The switch the product reads is stripped from every daemon
  it spawns, and a guard keeps it to two files. So no test can force a failure *between* two steps
  of one daemon-side publication; daemon-side cuts are real kills or file mutations, and "disk full"
  in the recovery records is a forced write failure at a hook-side site, never a real `ENOSPC`.
  Anything said about disk-exhaustion behaviour rests on a simulation.
- **Negative-knowledge blind mode is not observable through the seam** (R5-3). `doctor` classifies
  the ledger by reading its files rather than by opening it, because the open path takes a
  create-and-append handle. The rows are correct; the mechanism is indirect.
- **On Windows, a daemon lock cannot be told live from stale by inspection alone** (R5-4). The
  staleness window in §5 is how a dead holder is reclaimed; `fsck`'s daemon row reports a lock
  nothing answers behind as STALE and disables no check.
- **On Windows, durability across a power cut rests on NTFS journaling (owner decision D24).** On
  POSIX every promise Qompack makes — a sealed checkpoint, an acknowledged delivery, a published
  capture, a pin, a recorded elimination — is backed by a sync of the file and of the directory that
  names it ([Architecture §4](architecture.md#4-publication-and-durability)). On Windows the
  directory sync is a no-op. NTFS journals metadata, not file contents: every creation, rename,
  deletion and size change is one logged transaction, so a power cut never tears a directory entry,
  while bytes written since a file's last flush can be lost. Each of those promises ends with a file
  flush, which writes the file's bytes and forces the journal out past the directory change it
  depends on. What a power cut can still take is what follows the last flush on the volume: the
  most recent replacement of a derived file (a `state/` document, `pins/invariants.json`, a draft, a
  restore's final rename) can revert to its previous complete version, and a removed file can
  reappear. A backup's certification is exposed the same way: the rename of its manifest and the
  removal of its certification-pending marker are the last steps of `qompack backup`, no flush
  follows them, and a power cut just after the command reports the backup certified can leave it
  without its manifest or with the marker back. Verification refuses such a backup, so it is never
  restored from as certified; it must be taken again. Nothing Qompack guarantees depends on more than
  that. The premise has not been tested with a real power cut.
- **One platform, one host.** Every measurement on this page is windows/amd64 with one Claude Code
  version. The five other release targets are cross-compiled and untested at this level — see
  `docs/release.md` for the supported-scope table, which is generated from records rather than
  written by hand.
- **No timing claim is made here.** Nothing on this page asserts a latency, a throughput or a
  storage ratio.

## 9. Network and telemetry

**Qompack talks to nobody.** The authority is `test/guards`' `TestGuard_NoNetworkImports`, which
proves it of the import graph for the whole tree in both directions and permits the bare `net`
package in `internal/ipc` alone, for the local transport. CI re-checks the same property with an
import allow-list over `internal/**` and `cmd/**`.

`runtime.telemetry.enabled` exists only to say telemetry is off. It is hardwired: a configuration
that sets it true is refused, the effective configuration reports false, and the refusal is recorded
in `state/config-violations.json`. Measured on the packaged binary.

**Any future external service would require explicit user consent and a new architecture decision.**
Nothing in this build has one, and no configuration key can create one.

## 10. Reporting a problem

Open an issue with the output of `qompack doctor` and, if it is about integrity,
`qompack fsck --json`. Neither carries file contents or credential material; both name paths on your
machine, so read them before attaching. Do not attach `.qompack/objects/` or an eval export.
