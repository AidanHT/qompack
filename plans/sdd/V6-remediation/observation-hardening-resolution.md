# Coordinator resolution of observation hardening review

Implementation in progress, 2026-09-21; no release sign-off. The independent
review read an earlier source snapshot. Its original findings stay preserved.

H1: retain both the caller's original requested target IDs and the selected target
identities in sidecar v3 (unshipped). A known observation compares the full original
request and reuses the original selection; it never recomputes against current
supersession state. A changed requested set remains a conflict. Merely comparing
record ID, as the review's proposed shortcut suggests, would reintroduce the earlier
caller-modified-operation defect and is not accepted.

H2: require a final LF before a sidecar line can become authority. A complete JSON
value without its terminator is unavailable and cannot be followed by another append.
H3: reject non-regular sidecars before read-open; pin the index through the store
root and reject an aliased index directory. Linux FIFO/symlink cases are separate
from Windows results. os.Root permits links contained within its root; it does not
refuse every symlink, contrary to the review's phrasing.
H4: Close drains obsPubMu; publishers/writers recheck lifecycle after obtaining the
barrier. MarkSuperseded uses the same barrier so a later mark cannot slip between
an intent's target snapshot and completion. No normal record-plus-marks batch is split.
H5: Recover verifies/syncs the original object closure even for a load-derived
committed binding. Pure read-only lookup remains a metadata lookup.

Selected targets now bind immutable record metadata (ignoring only mutable
supersession status/By), not just content root. Another delivery repeating a
compatible host ID binds to the original record and performs no fresh supersession,
preserving the existing repeated-host-ID regression. Different origin identity is
refused. Observation IDs require the canonical nonzero digest string, and embedded
legacy-record versions are validated.

`observation-reopen-close-regressions` passed all selected store cases but exposed
the repeated-host-ID integration regression. `observation-compatible-host-replay`
then passed the relevant store and observer cases. The failed runs and old
assertion/review mappings remain. Later validation uses immutable source identifiers.

The observation sidecar still has an explicit lifetime byte/entry ceiling and
fails closed at capacity. This does not certify unlimited publication capacity;
journal rollover alone cannot establish that broader claim. Unknown/torn writes
require reopen/verified recovery, never automatic truncation. All installed-package,
platform and human release obligations remain separate.
