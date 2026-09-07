---
paths: ["src/api/**"]
description: REST layer conventions
---
# API conventions

Every handler validates its request body with zod, and returns a discriminated
`{ ok: true } | { ok: false, error }` envelope. Never throw across the route boundary.
