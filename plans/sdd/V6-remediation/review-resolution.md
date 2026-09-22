# Coordinator adjudication of independent findings

The original review files are retained as written. Their conclusions are source review, not
runtime certification.

- Recovery R1/R2/R3/R5 were corrected and independently re-reviewed. Operator verification,
  restore and fsck refuse an uncertified snapshot; creation/restoration stream; source opens are
  avoided for verification/restoration; native no-replace publication has no stale lockfile.
- F1's suggested direct-IPC WAL exception is rejected. `dispatchOp` runs admission before
  `callHandler`, whose observe route is the only route to ingest.Accept. A direct IPC regression
  asserts an outside capture never reaches the WAL. An internal unit calling Accept directly is
  not an exposed IPC endpoint. Inherited old spool content remains subject to drain admission.
- F2's envelope assumption is covered by complete-object, reordered/truncated, byte-free evidence,
  forged Capture.Bytes and real hook admission tests. Opaque prefixes cannot prove scope.
- F3: Grep/Glob can carry structured paths; those paths are scoped. Shell command text is not a
  substitute file permission parser.
- F4: an uncertified snapshot intentionally cannot be promoted by deleting a flag through an
  operator command. Preserve it and create a new consistently leased backup with a fresh ID.
- F5: restore staging no longer calls EnsureLayout. It copies only manifest files and creates
  their parent directories. Read-only proof creates no layout content.
- F6: staged copy receives its root explicitly for the append-only guard.

Native host Read parity remains unverified. These fixes do not grant host authorization, and no
review clears the release gate or the twelve pending human UAT scenarios.
