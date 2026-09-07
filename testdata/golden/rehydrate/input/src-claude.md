Everything under `src/` is TypeScript with `strict` on and no exceptions granted. The compiler is
the cheapest reviewer available and it works nights.

- Modules export named symbols. There are no default exports; a default export renames itself at
  every import site and makes a symbol search useless.
- Imports are absolute from `src/`. Relative paths deeper than one level are a refactoring hazard
  and are rewritten on sight.
- `any` requires a comment naming the boundary it crosses. `unknown` plus a narrowing function is
  almost always what was meant.
- Errors are classes extending `AppError` and carry a machine-readable `code`. Throwing a string
  or a bare `Error` loses the mapping the middleware depends on.
- Async functions return `Promise<T>`, never `Promise<T | undefined>` as an error channel.
  Absence and failure are different, and the type should say which one happened.
- No module has side effects at import time beyond declaration. A module that connects, reads a
  file, or starts a timer on import cannot be tested and cannot be tree-shaken.
- Configuration is read once at startup into a frozen object. Reading `process.env` below the
  entry point makes the configuration surface unknowable.
