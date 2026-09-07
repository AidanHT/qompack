Anything that opens a connection, holds one, or changes what a connection means belongs here.
`src/db/` owns the pool; no other package constructs one.

1. There are exactly two pools: `request` and `auth`. A third pool requires an architecture note,
   not a pull request.
2. Pool sizes are set from `DB_POOL_REQUEST` and `DB_POOL_AUTH` with no defaults. A missing
   variable is a startup failure, deliberately, so that a misconfigured deploy never half-works.
3. `withTransaction(fn)` is the only way to open a transaction. It sets a statement timeout, tags
   the connection for tracing, and guarantees release on both paths.
4. The callback passed to `withTransaction` must not await anything that is not a database call.
   A lint rule enforces this; suppressing it requires a comment naming the incident you expect.
5. Transactions are short. The p99 budget is 50 ms; a transaction routinely exceeding it is a
   design problem and gets a ticket, not a longer timeout.
6. Read-only work does not open a transaction. A single statement is already atomic and the
   BEGIN/COMMIT round trip doubles its cost.
7. Isolation is READ COMMITTED everywhere. A stronger level is a per-call argument with a comment
   explaining the anomaly it prevents.
8. Advisory locks are forbidden in request paths. They are permitted in jobs, where a stuck lock
   is an alert rather than an outage.
9. Every query is a prepared statement built by the query builder. String interpolation into SQL
   fails the security job and is not overridable.
10. `SELECT *` is forbidden. Column lists are how a migration that drops a column is caught at
    compile time rather than at 3am.
11. Every table has `id`, `created_at`, and `updated_at`. `updated_at` is maintained by a trigger,
    never by application code, because application code forgets.
12. Soft deletes use `deleted_at` and every query against a soft-deletable table goes through the
    scoped repository. A raw query against such a table is a data-leak bug.
13. Migrations are forward-only and are never edited after merge. A wrong migration is corrected
    by a new migration.
14. A migration that takes a lock on a table with more than a million rows must be split: add the
    column nullable, backfill in batches, then add the constraint.
15. `NOT NULL` is added with `NOT VALID` first and validated in a second migration. The one-step
    form takes an ACCESS EXCLUSIVE lock for the length of a full scan.
16. Indexes are created `CONCURRENTLY` and therefore never inside a transaction. The migration
    runner knows about this; do not fight it.
17. Foreign keys are declared with an explicit `ON DELETE` action. The default is a silent
    RESTRICT that surfaces as an opaque constraint violation months later.
18. Connection acquisition is instrumented. `db.pool.wait_ms` is the metric that predicts an
    outage; alert on its p99, not on its mean.
19. When pgbouncer runs in transaction pooling mode, session-level state does not survive a
    statement. No `SET`, no session advisory locks, no `LISTEN`, no server-side cursors.
20. `statement_timeout` set from the client is ignored in transaction pooling mode. Timeouts must
    be configured on the pgbouncer side or they do not exist.
21. Prepared statement names must be unique per connection or pgbouncer will reuse the wrong plan.
    The query builder handles this; hand-written `PREPARE` does not.
22. The reproduction for any pool-related change is `test/load/refresh-storm.ts`. A pool change
    merged without it is reverted on sight.
23. Never tune a pool to fix a lock-hold-across-IO pattern. The pool size moves the cliff; it does
    not remove it, and the next traffic increase finds it again.
