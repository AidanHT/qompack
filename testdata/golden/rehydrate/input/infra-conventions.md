`docker-compose.yml` is the reproduction environment. It is not a development convenience and it
is not a deployment artifact; it is the file that decides whether a bug reproduces.

1. Every service pins an exact image tag. `postgres:16` is forbidden; `postgres:16.4-bookworm` is
   required. A floating tag makes a reproduction unreproducible a week later.
2. The pgbouncer `pool_mode` is `transaction` and matches production. Changing it locally to make
   a test pass is changing the test, not fixing the bug.
3. `max_client_conn` and `default_pool_size` are set explicitly and are the production values
   divided by the documented ratio in `infra/README.md`.
4. Resource limits are declared for every service. An unlimited container hides a memory leak
   until it reaches a machine that is not the developer's.
5. Health checks are real queries, not TCP probes. A port that accepts connections while the
   database is still recovering is the worst kind of green.
6. `depends_on` uses `condition: service_healthy`. The bare form only orders start, not
   readiness, and the difference is a flaky suite.
7. Volumes for data are named, never bind-mounted from the host. A bind mount on macOS changes
   the fsync behaviour and therefore the timing the bug depends on.
8. No service publishes a port unless a human needs it. Published ports collide across projects
   and the collision presents as an unrelated connection error.
9. Environment values live in `.env.example` with every key present and every secret blank. A key
   absent from the example file is a key someone will not set.
10. Secrets never appear in this file, not even as defaults, not even for local use. A default
    that works is a default that reaches production.
11. The compose file and the load test are versioned together. If `refresh-storm.ts` needs a new
    service, the same commit adds it here.
12. Any change to this file invalidates the eliminations that depend on it. The negative-knowledge
    ledger tracks that dependency; do not work around a stale flag by deleting the record.
13. `restart: unless-stopped` is set on infrastructure services and never on the application. An
    application that restarts silently hides a crash loop from the test run.
14. Logging drivers are left at their defaults. A JSON log driver with rotation is a production
    concern and it changes the flush timing under load.
15. The compose project name is pinned so that two checkouts of this repository do not share
    volumes. They will, otherwise, and the resulting state corruption is very hard to read.
16. `docker compose down -v` is part of the reproduction protocol, not an optional cleanup. A
    retained volume carries the previous run's pool statistics into the next one.
