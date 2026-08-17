# Session decision records

A subplan's *rulings* are the decisions its implementing session had to make because the plan and
the shipped code disagreed — a name the plan spells one way and SP-01 shipped another, a commit
order that cannot compile as written, a struct the plan wants at a path another component already
owns. They are not design; the design is `Qompack.md` and `plans/00-ARCHITECTURE.md`. They are the
record of *why the branch does not match its own plan in the places where it does not*, and without
them the next person to read the diff has to re-derive the reasoning or, worse, "fix" it back.

`plans/README.md` requires that an implementer who hits an ambiguity fix the plan rather than
improvise silently. A ruling is the audit trail for that repair.

## What is here

| Directory | Subplan | Why it is committed here |
|---|---|---|
| `V2-SP-05-daemon-ipc-and-hot-path/` | SP-05 | Its session kept its ledger under an untracked, git-ignored path and wrote no tracked handoff document. `V2-VERIFY` gate **V2-MERGE-20** required it to be secured before `verify/v2` was cut |

Every other wave-1 subplan recorded its reconciliation somewhere already tracked: SP-02 and SP-07 in
`plans/V2-SP02-handoff.md` and `plans/V2-SP07-handoff.md`, SP-04 in `plans/CARRIED-DEFECTS.tsv` plus
`plans/V2-SP-04-carried-defects.md`, SP-03 and SP-06 in their sections of
`plans/V2-VERIFY-primitives-store-dag-and-baseline.md` (§2.3a and §2.6a).

## How to read SP-05's ledger

- `progress.md` — the ledger proper. A task map, a pre-flight conflict scan (plan text vs. repo
  reality) and a running task log. Each ruling states what was found, what was decided, and **the
  cost if the ruling is wrong**. `V2-VERIFY` §2.5a is the summary; this is the reasoning.
- `task-N-spec.md`, `task-N-brief.md`, `task-N-report.md` — what each of the seven tasks was asked
  to do and what it reported doing.
- `task-N-review.md` and `review-task*.diff` — the review of each task and the diff it reviewed.
  The diffs are of intermediate working states, not of commits, so they are **not** reconstructible
  from git history. That is why they are here rather than left to `git log`.
- `final-review.md`, `final-branch-summary.txt` — the whole-branch review and its summary.
- `sp01-inventory.md`, `plan-context.md`, `plan-interfaces.md` — the inputs the session worked from.

## Adding to this directory

Copy the session's record in whole, unedited, at the point its branch merges. A summarised or
partially-copied ledger is worse than none: it reads as complete while the ruling someone needs is
the one that was trimmed.
