# C5.5 on candidate 8: the known-defect statement and its notes

The confirmatory run (pre-registration amendment A8) on candidate 8, `3ec62ad2`, uses the frozen bundle
`qompack-bundles/c8/qompack-plugin-0.3.0-windows-amd64` (BUNDLE.json sha256
`61ba9c37dda03c14c44acb6824646a8d7410751bba6f1ce3c5c864d6382dcd8b`, as in `phase3/c8-CANDIDATE.md`). It
states `--known-open-defects none` (D75(e)).

- No `plans/CARRIED-DEFECTS.tsv` row is open. Every row is `fixed` or `wontfix`.
- No defect in `plans/V6-CLOSEOUT-CHECKLIST.md` lacks a fix or a recorded disposition. Its unchecked items
  are verification and release steps, not defects.

A8 item 4 lets the residuals accepted by a recorded decision be listed in the run's notes instead of the
flag. `devtool live-eval` treats any ID named in the flag as an open defect, so they are listed here. None of
them makes the run non-confirmatory:

- **D6:** rollover residuals (a bounded rotation pause every 65,536 deliveries; GC halts safely).
- **D29:** D21's borrow edge at the borrow limit.
- **D35(c):** a SessionEnd flush that meets a stopping daemon waits in the spool until the next session.
- **D38:** SP08-D3's residual pid-reuse case.
- **D44:** spool submode does not recover within a session.
- **D48:** safeCut's non-maximal cuts.
- **The C2.8 rulings (D54):** SP06-D2 and SP08-D1 are wontfix for 0.3.0.
- **Candidate 8's known issues 1 to 16:** from `plans/sdd/V6-closeout/w22-known-issues.md` (D66(d), D67(o),
  D72, D73, D75). Its test-only residuals are test defects, not product defects.

Order (A8 item 5):
1. The dry run with `--confirmatory` printed "confirmatory preconditions at plan time: met" on 2026-10-07
   (run id 20261007T145415Z-1497b6, plan only).
2. The run itself starts only after candidate 8's live re-check (`wf_090ea750-b1e`) has passed.
