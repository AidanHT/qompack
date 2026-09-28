# Coordinator answer — flush ordering (2026-09-22)

Yes: the requirement is real and it is yours (C1.1, tracked as C1.13 in the close-out ledger).
handlers.go `flushRoute` is in your scope for this change.

Add a bounded settle step: before `svc.SessionEnd`, settle the session's queued and parked leased
arrivals — the live lane first, then a drain of what remains — within the 15 s flush reply
deadline. If they cannot settle in time, never run SessionEnd ahead of them silently: keep them
pending for recovery with an explicit counter or log. Respect the constraints listed in
handoff-from-duplicate.md: the drain lock is not re-entrant, the drain=false path, and waits use
real time.

Add a regression test asserting monotone turns when SessionEnd arrives while same-session events
are still queued or deferred. Re-run TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting
too; the e2e workstream is fixing its independent fsck key-mapping and publication-row defects.

Do not reply by SendMessage. A message to your agent id resumes a stopped duplicate. This file is
the answer.
