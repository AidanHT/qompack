This file is the security review checklist in rule form. It is long on purpose: a checklist that
fits on a screen is a checklist someone has already decided they remember.

## Boundaries

1. Untrusted input is anything that crossed a process boundary: a request body, a header, a
   database row written by a previous version, an environment variable, a file on disk, the
   output of a subprocess, and the response of an internal service. "Internal" is not a trust
   level.
2. Every boundary has exactly one parser, and the parser produces a typed value. Code downstream
   of the parser never re-validates, because two validators disagree eventually and the
   disagreement is the vulnerability.
3. Validation rejects; it does not repair. Trimming, coercing, or normalizing an invalid value
   produces a value the sender did not send and nobody reviewed.
4. Length limits are declared on every string field that reaches storage, a log line, or another
   service. An unbounded field is a denial-of-service primitive with extra steps.
5. Collections have element limits as well as byte limits. A 1 KB body containing 50,000 empty
   array entries is small and expensive.
6. Recursion limits are explicit wherever the input shape is recursive. The default stack is not
   a limit, it is a crash.
7. Decompression is bounded by output size, not by input size. A zip bomb is small on the wire by
   definition.

## Identity and authorization

8. Authentication answers "who"; authorization answers "may they". A handler that checks only the
   first has checked nothing about the object it is about to return.
9. Authorization is checked against the object, not against the route. `GET /users/:id` that
   verifies a valid session and not the relationship between session and `:id` is the single most
   common serious bug in this codebase's history.
10. Deny by default. A new route with no policy is unreachable until a policy is written, and the
    router enforces that at startup rather than at request time.
11. Policies are data, tested independently of the handlers that consume them. A policy expressed
    only as branching inside a handler cannot be reviewed as a policy.
12. Elevation is explicit, scoped, and logged. There is no ambient admin context that a helper
    three layers down can pick up.
13. Service-to-service calls carry the caller's identity, never a shared secret that means
    "trusted". A shared secret makes every service as privileged as the least careful one.
14. Tokens are audience-scoped. A token minted for one service must be rejected by another even
    when both trust the same issuer.
15. Impersonation, where it exists at all, records both identities in every log line and every
    audit row. A record that shows only the impersonated user is a record that hides the actor.

## Secrets

16. Secrets come from the secret manager at startup and live in memory. They are not in the
    repository, not in the image, not in the compose file, and not in a comment explaining where
    they used to be.
17. A secret that has ever been written to a log, a ticket, a chat message, or a terminal
    recording is burned. Rotate it; do not reason about who saw it.
18. Secret material is compared with a constant-time primitive and never logged, never included
    in an error message, and never used as a map key that might be dumped.
19. Key rotation is supported before a key is first used, not after it must be rotated. Rotation
    designed under incident pressure is rotation done wrong.
20. Derived keys are per-purpose. One key used for signing and encryption is a key whose
    compromise costs twice as much.
21. Randomness for anything security-relevant comes from the CSPRNG. `Math.random` in a security
    path fails the security job and is not overridable by review.
22. Nonces are never reused with the same key, and the code makes reuse structurally impossible
    rather than merely unlikely.

## Data handling

23. Personal data is classified at the schema, and the classification is what the retention job
    and the export job both read. A field nobody classified is a field nobody will delete.
24. Logs carry identifiers, not contents. A user id is an identifier; an email address is
    contents; a request body is contents with a plausible excuse.
25. Errors returned to a client name what the client did wrong and nothing about what the server
    is. Stack traces, query text, and internal hostnames do not cross the boundary.
26. Timing differences that depend on secret material are bugs. Where a constant-time comparison
    is impossible, the operation is made uniformly slow rather than conditionally fast.
27. Enumeration is prevented at the response level, not by obscurity. If "user exists" and "user
    does not exist" produce different bodies, different statuses, or different latencies, the
    endpoint is an enumeration oracle.
28. Bulk export is rate-limited and audited separately from ordinary reads. The interesting attack
    is rarely one request.
29. Deletion is real. A soft delete that leaves the row readable through any query path is not a
    deletion and must not be described as one to a user.

## Dependencies and supply chain

30. Dependencies are pinned by exact version and integrity hash. A range is a promise that
    somebody else's future decision is safe.
31. A new direct dependency requires a note naming what it does, what it replaced, and who
    maintains it. Transitive additions are reviewed in aggregate at upgrade time.
32. Build steps do not fetch from the network. A build that can reach the internet can be changed
    by the internet.
33. Lockfiles are committed and their diffs are read. An unexplained lockfile change is a review
    blocker, not a formality.
34. Postinstall scripts are disabled. A dependency that requires one is a dependency that needs a
    conversation first.
35. Container images are built from a pinned digest, scanned on build, and rebuilt on base-image
    advisories rather than on a schedule.

## Operational

36. Every security-relevant event emits a structured record with actor, action, object, and
    outcome. Four fields, always the same four, so the queries are writable.
37. Audit records are append-only and are not deletable by the application's own credentials.
38. Alerting is on the absence of expected events as well as on the presence of unexpected ones.
    A silent audit stream is an outage nobody pages for.
39. Rate limits are per-principal and per-object, not only per-IP. An authenticated attacker has
    an IP address too, and it is usually a good one.
40. Lockout is a last resort and is never triggerable by an unauthenticated third party against
    another user's account.
41. Incident response starts by preserving evidence: capture before you restart, and record what
    you captured before you change anything.
42. Any change to this file requires a security review sign-off recorded in the pull request. The
    sign-off names the reviewer, not the team.
43. A finding is closed by a test that would have caught it, or by a written argument for why no
    test can. "Fixed" with neither is reopened.
