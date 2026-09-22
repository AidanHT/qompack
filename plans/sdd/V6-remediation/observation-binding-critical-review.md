# V6 remediation — observation-binding decision: source/contract review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** source/contract review only — no edits, tests, builds, git mutations, config changes.
Only output: this file. Rollover redesign stays blocked and is not touched here; this is the smaller
private-index metadata question.

**Read:** `observation-binding-decision.md`; `00-ARCHITECTURE.md` §0.1 and §0.2; current
`internal/store/tooluse.go`, `internal/store/tooluseindex.go`; the private golden
`testdata/golden/store/tool_use.jsonl` and its guard `internal/store/golden_test.go`; `fsstore.go`
(`indexRecordVersion`); `test/e2e/v3_x09_test.go` tool_use readers.

---

## 1. Verdict

The mechanism (an `omitempty observation_id` on the PRIVATE `tuRec` line, plus a secondary
observation→id map) **does close the record-before-sidecar cut in one append with no reservation
protocol**, and it can be made **fixture- and old-reader-compatible** — but only under three exact
conditions (§2–§4). There is **one real blocker, and it is architectural, not a shipped guard:**
`00-ARCHITECTURE.md §0.2` (the user-authorized v1.5 amendment) mandates that observation identity
binds through a **dedicated sidecar `index/observations.jsonl`**, that "every new field is a versioned
sidecar," and that `tooluse.go` wire/seams stay frozen — the opposite of growing the tool_use line
(§5). §0.2 is proposed/gated/pending SP-20 approval, so this is an ownership/authorization blocker to
return to main, and it collides technically with the decision's "one append" goal (§6).

---

## 2. Exported wire/fixture — untouched (verified)

The decision keeps `ToolUseRecord.MarshalJSON`, `toolUseRecordWire`, and the contract fixture
`testdata/golden/contracts/store/want/tool_use_line.jsonl` (long-key type wire, Rule W-2). The
in-process-only `Observation` field already exists on `ToolUseRecord` and is already omitted by
`MarshalJSON` (`tooluse.go:60-66`). Nothing in the decision changes that. ✓

## 3. Private-index golden — a real byte-freeze guard, SATISFIED by omitempty (the exact guard)

`internal/store/golden_test.go:81-99` `goldenIndexFile` is a **byte-equality guard**
(`bytes.Equal(wantLF, gotLF)`) over `testdata/golden/store/tool_use.jsonl`. Its scripted records
(`goldenToolUseA/B`, lines ~145-158) are written through `RecordToolUse` **with no `Observation`**,
and the golden lines end `…,"eph":false,"sub":""` — no `observation_id` key.

- With `json:"observation_id,omitempty"` on `tuRec` and a zero `Observation` for those fixture
  records, `json.Marshal` **drops** the key → the golden lines stay byte-identical → the guard passes.
  So this guard **does not forbid** the additive field; it freezes the EXISTING records' bytes, and an
  omitempty field carried only by records that actually have a value is not frozen against.
- It WOULD fail (and force a forbidden golden regeneration) if the field were **not** omitempty, if it
  were ordered so a golden record carried it, or on any reorder. **Required condition:** the field is
  strictly `,omitempty`, and `testdata/golden/store/tool_use.jsonl` must **not** be regenerated.

This is the guard the decision's "If an actual repository guard freezes tuRec's new-record shape,
stop" clause points at. It is a freeze of the RECORD BYTES, not of the schema against additive
optional fields, so it is **not a stop** — but it is the exact guard the author must keep green
without `-update`.

## 4. Old-reader compatibility — demonstrated, with one hard prerequisite

- `loadToolUse` (`tooluseindex.go:135-179`) decodes each line with plain `json.Unmarshal` into `tuRec`
  and `opProbe` — **no `DisallowUnknownFields`** — so an old private-index reader silently ignores an
  additive `observation_id`. The e2e reader `x9ToolUseLine` (`v3_x09_test.go:638-655`) likewise uses
  plain `Unmarshal`. Compatibility is demonstrated in source, not guessed. (No `DisallowUnknownFields`
  guard exists on any tool_use line; the ones in the tree are on unrelated fixtures — analyzer,
  replay, v3_x05, v5_x17.)
- **Hard prerequisite:** `indexRecordVersion` (`fsstore.go:47`) must stay `1`. `loadToolUse` skips any
  line whose `probe.V != indexRecordVersion` (returns `false` → counted `bad` → record dropped). If
  the author bumps the version to "mark" the new field, **every new line is skipped by old readers and
  the record is lost** — the opposite of additive compatibility. Do not bump it.

## 5. THE BLOCKER — architecture mandates sidecar-only binding (§0.2), exact source

`00-ARCHITECTURE.md §0.2` (the user-authorized v1.5 migration amendment) is explicit that the
observation↔record durable join lives in a DEDICATED sidecar, not on the tool_use line:

- **Line 64:** "The sidecar is `index/observations.jsonl` (append-only, keyed by `ObservationID`, with
  optional nonunique `HostID` and legacy `ToolUseID` lookup fields); **`ToolUseRecord`'s wire line …
  keep their bytes.**"
- **Line 52:** "**every new field is a versioned sidecar read through a compatible reader.**"
- **Line 104:** "**`tooluse.go` wire and existing seams frozen**; capture before transform" — under the
  SP-20 storage owner.
- **Line 61:** "an additive contract cannot read an old missing field as complete evidence" — the
  additive-sidecar model, not a grown line.

The decision places `observation_id` ON the private tool_use line — the surface §0.2 designates
frozen, and the field-on-a-sidecar rule inverted. `index/tool_use.jsonl` IS the durable on-disk
"wire line" §0.2 says keeps its bytes (arch line 608), so a NEW leased record's line growing an
`observation_id` key is contrary to §0.2's "new field = sidecar." The decision doc's compatibility
argument addresses only the FIXTURE distinction (private `tuRec` vs the exported-type fixture); it
does not address §0.2's separate mandate that observation identity binds through
`index/observations.jsonl` with the tool_use wire frozen.

**Why this is a return-to-main blocker, not a hard shipped stop:** §0.2 is a **proposed** amendment,
gated behind `runtime.migration.*` (which `config.Validate` refuses while pending, arch line 121),
and it **requires SP-20/SP-13/SP-10/SP-11 approval before any owner implements against it** (arch
line 52). So `index/observations.jsonl` is not shipped, and no currently-ENFORCED guard blocks the
tuRec field. But the observation join is SP-20's storage-owner scope, and this decision pre-empts
SP-20's designated observations sidecar and grows a line §0.2 froze — **without the owner countersign
§0.2 demands.** That reconciliation is main's to obtain, not V6-remediation's to take unilaterally.

## 6. The technical conflict main must resolve

The decision's entire rationale is **one-append** cut closure: the binding must land in the SAME write
as the record so a crash cannot separate them. A dedicated `index/observations.jsonl` sidecar (the
§0.2 home) is a DIFFERENT file and **cannot be written atomically with the tool_use line** — using it
reintroduces the two-write cut the decision is trying to remove. So §0.2's sidecar mandate and the
one-append goal are **mutually exclusive**; main cannot satisfy both. The options:

1. Grow the tool_use line (as decided) — accept a deliberate, documented V6 exception to §0.2's
   "new field = sidecar" rule, with SP-20 countersign, and reconcile it against SP-20's future
   `observations.jsonl` so the join does not end up with two competing durable homes.
2. Close the cut WITHOUT one-append atomicity — e.g., write the observation-binding sidecar line
   BEFORE the tool_use record so the binding is recoverable from the sidecar side if the record write
   is lost; or reuse the EXISTING capture sidecar. This honors §0.2 but is a different crash argument
   than "one append."

**Overlap to flag:** the prompt path already closes this same record-before-sidecar cut WITHOUT
growing the wire, via the observation-bound `ArgsDigest` + `PromptFrontier` recovery (see
`prompt-critical-review-followup.md`). This decision generalizes the binding to all tool_use records
via a real field. Main should pick ONE mechanism (ArgsDigest-binding vs `observation_id` field), not
ship both competing bindings for the same join.

## 7. Old-writer / GC downgrade limits (as asked)

- An OLD writer re-recording an existing ID with the SAME Root is a silent no-op (`RecordToolUse`
  dedup by ID+Root, `tooluseindex.go:227-234`); it never rewrites a line, so it cannot strip
  `observation_id` from a new record — but neither can a new writer add the binding to a line an old
  writer already wrote (append-only, no rewrite). Such records stay **unbound** for replay recovery.
- GC and readers key on ID/Root and ignore the extra field. Unaffected.
- The decision's "No older writer/downgrade support is inferred" and "old readers may display content
  but do not gain this replay guarantee" (decision §) correctly scope this. It matches the "legacy
  unlinked records lack the binding" limitation already recorded for the prompt work; keep it out of
  any exactly-once claim.

## 8. Non-acceptance

Source/contract review only; no clearance. The mechanism closes the cut without a reservation
protocol and is fixture/old-reader compatible **iff** the field is strictly `,omitempty`,
`indexRecordVersion` stays `1`, and `testdata/golden/store/tool_use.jsonl` is not regenerated (§3, §4).
The exported wire/fixture is untouched. The blocker is `00-ARCHITECTURE.md §0.2` (lines 52, 64, 104):
it mandates observation binding in a dedicated `index/observations.jsonl` sidecar with the tool_use
wire frozen — a proposed/gated contract owned by SP-20 — so growing the tool_use line pre-empts that
owner and must be countersigned and reconciled with SP-20 (§5), and the sidecar-vs-one-append conflict
(§6) and the ArgsDigest-vs-field overlap (§6) are main's to resolve, not this decision's to assume.
Rollover redesign remains separately blocked.
