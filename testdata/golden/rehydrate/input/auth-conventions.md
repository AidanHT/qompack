`src/auth.ts` is the only file permitted to mint, rotate, or revoke a credential. Everything else
asks it to. The rules below exist because every one of them has been violated at least once and
the violation cost a production incident.

1. Refresh-token rotation is single-use. A token presented twice is a reuse event, not a retry,
   and reuse detection revokes the entire family. This is a security requirement, not a
   performance tuning knob.
2. The rotation writes the new token and marks the old one consumed in one transaction. There is
   no window in which both are valid and no window in which neither is.
3. The identity-provider call happens OUTSIDE that transaction. Holding a row lock across an
   outbound call is what exhausted the pool; the fix is structural, not a timeout.
4. `refreshToken()` acquires a dedicated short-lived connection from the `auth` pool, not the
   request-scoped connection. The two pools are sized independently for exactly this reason.
5. Never widen a pool timeout to fix a pool exhaustion. Under pgbouncer transaction pooling,
   `statement_timeout` is ignored, so the widened value never takes effect at all.
6. Access tokens are 15 minutes. Refresh tokens are 30 days. Neither is configurable at runtime;
   changing one is a code change with a migration for the existing rows.
7. Token bodies carry `sub`, `sid`, `iat`, `exp`, and nothing else. Every additional claim is a
   field that must be revoked when it changes, and revocation is the expensive path.
8. Session identifiers are opaque to every consumer. Do not parse a `sid`, do not sort by it, and
   do not infer creation order from it.
9. All comparison of secret material uses `timingSafeEqual`. A `===` on a token, a signature, or
   a hash is rejected in review regardless of the surrounding argument.
10. Hashes of refresh tokens are stored, never the tokens themselves. A database dump must not be
    sufficient to impersonate a user.
11. The hash is SHA-256 over the raw token with no salt, because the token is already 256 bits of
    entropy. Do not "improve" this to bcrypt; it makes the lookup a table scan.
12. Revocation is by family, not by token. Revoking one token of a family and leaving its
    siblings valid is the bug reuse detection exists to catch.
13. A revoked family is retained for 90 days so that a reuse event can still be attributed. The
    retention job lives in `src/jobs/auth-gc.ts` and must not be merged into the general GC.
14. Clock skew tolerance is 30 seconds in each direction and is applied only to `exp`, never to
    `iat`. A future-dated `iat` is a forgery signal.
15. Every auth failure emits a structured event with `reason`, `sid`, and `ip`. The reason is one
    of a closed set; free-text reasons make the alerting rules unwritable.
16. Never emit the token, the hash, or any prefix of either into a log line, a trace attribute, or
    an error message. A four-character prefix is enough to make a leak actionable for an attacker.
17. Rate limiting on the refresh route is per-family, not per-IP. A shared NAT should not be able
    to lock out an unrelated user.
18. The 401 body for an expired token and the 401 body for an invalid token are byte-identical.
    Distinguishing them is an oracle.
19. Tests use the fixture clock in `test/support/clock.ts`. A test that calls `Date.now()` is
    flaky by construction and will be quarantined.
20. Every change to this file requires re-running `test/load/refresh-storm.ts` at 200 concurrent
    refreshes and attaching the result. The reproduction is the review artifact, not the diff.
21. The pgbouncer pool mode is a dependency of these rules. If `docker-compose.yml` changes its
    `pool_mode`, this file is stale until the reproduction has been re-run against the new mode.
22. Do not add a cache in front of the token lookup. The lookup is a primary-key hit; a cache adds
    a revocation window and buys nothing measurable.
23. Do not add retries around the identity-provider call inside the request path. A retry there
    turns a slow dependency into a queued one and the queue is the pool.
