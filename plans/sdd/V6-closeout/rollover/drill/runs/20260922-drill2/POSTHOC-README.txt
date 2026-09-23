posthoc-* files were collected after drill2 finished, by hand, from the drill's own container
directory /work/cx-rollover-drill-20260922-drill2 (nothing there was modified):

  posthoc-control-fsck.json / posthoc-projectA-fsck.json
      `cur fsck --project <p> --json` (read-only) on the never-rotated control project and on the
      rotated project A. Both fail the same two checks, captures ("capture publication requirement
      is unknown") and publication ("capture sidecar with an unrecognized op"); the delivery row
      passes on both. The restore integrity failure in backup-restore.json is exactly these rows.
  posthoc-control-flush-capture-sidecar.json
      the capture sidecar behind that failure: the SessionEnd `flush` delivery's sidecar, op "flush",
      which internal/store/publication_audit.go classifyCaptureView does not recognise. The control
      never rotated, so this is the base's (verify/v6 cf31e01), not the rollover's.
  posthoc-projectA-daemon-log.txt / posthoc-control-daemon-log.txt
      both daemons hold every capture from session registration until the SessionEnd flush
      (control 00:24:00 -> 00:26:06, producer 00:26:28 -> 00:28:33); the producer's two
      "tool capture unpublished" / "context deadline exceeded" lines fall in the backlog the drill
      then SIGTERMed. drill3 waits for that backlog before stopping.
