Every route under `src/api/` is a thin adapter. It parses, it authorizes, it delegates, and it
maps a domain error onto a status code. Business logic that lives in a route handler is a bug
report waiting to be filed against the wrong file.

1. One route per file, named for its path: `POST /api/session/refresh` lives in
   `src/api/session/refresh.ts` and exports exactly one handler.
2. The handler signature is `(req: Request, ctx: RouteContext) => Promise<Response>`. Nothing
   else is exported from a route file — no helpers, no constants, no types other callers need.
3. Parse the body with the route's own zod schema before touching anything else. A handler that
   reads `req.body.foo` without having parsed is rejected in review without further comment.
4. Authorization is the second statement, never the last. `ctx.requireSession()` throws
   `UnauthorizedError`, which the error middleware maps to 401; do not hand-roll the check.
5. Delegate to a service in `src/services/`. A route may call at most two service functions. If
   it needs three, the orchestration belongs in a service of its own.
6. Never open a database transaction in a route. Transactions are a service concern, and the
   pool-exhaustion incident of 2026-01-01 was a route holding one across an outbound call.
7. Never `await` an outbound HTTP call while a transaction is open, anywhere. This is the single
   rule with an incident attached to it; see `docs/incidents/2026-01-01-pool-exhaustion.md`.
8. Return `Response.json(body, { status })` directly. There is no `res.send`, no `res.status`,
   and no framework wrapper to learn.
9. Domain errors are classes in `src/errors/`. The error middleware owns the mapping from class
   to status; a route that constructs a status code for a domain condition has bypassed it.
10. A 5xx is never returned deliberately. If the handler can name the failure, it is a 4xx with a
    machine-readable `code`; if it cannot, it throws and the middleware owns the 500.
11. Every response body has a `code` field on the error path and no `code` field on the success
    path. Clients switch on `code`, never on the human-readable `message`.
12. `message` is for humans and may change without notice. `code` is an API contract and changing
    one is a breaking change requiring a version note.
13. Idempotency keys are read from the `Idempotency-Key` header, never from the body, and are
    required on every non-GET route that has an external side effect.
14. Pagination is cursor-based. `limit` is clamped to 100 server-side; a client asking for more
    gets 100 and a `Warning` header, not a 400.
15. Timestamps in request and response bodies are RFC 3339 with an explicit offset. A bare
    `2026-01-01T00:00:00` is rejected at the schema boundary.
16. Money is an integer of minor units plus an ISO 4217 code. There are no floats anywhere in an
    API body, in either direction.
17. Enumerations are lowercase snake_case strings, never integers. An integer enum on the wire
    cannot be extended without a coordinated deploy.
18. Unknown fields in a request body are rejected, not ignored. A typo'd field name that silently
    does nothing is the most expensive kind of API bug.
19. Unknown fields in a response body are permitted and clients must ignore them. This asymmetry
    is deliberate and is what lets the API add fields without a version bump.
20. Every route emits one structured log line on completion with `route`, `status`,
    `duration_ms`, and `session_id`. Nothing else logs at info level inside a request.
21. Do not log request or response bodies. If a body is needed to debug, it is captured by the
    tracing sampler under an explicit flag, not by a `console.log` someone forgot to remove.
22. Rate limits are declared in the route's own module as `export const rateLimit`. A route with
    no declaration inherits the conservative default and that is usually correct.
23. Long-running work is enqueued, never awaited. A handler that can exceed two seconds under
    p99 returns 202 with a job id.
24. Tests for a route live beside it as `refresh.test.ts` and exercise the HTTP surface, not the
    handler function. Calling the handler directly skips the middleware the route depends on.
25. A new route is not merged without a load test entry in `test/load/routes.yaml`, even if the
    entry is one request per second. The absence of an entry is how routes escape the budget.
