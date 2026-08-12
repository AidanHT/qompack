// Package config implements Qompack's Appendix C configuration schema: typed defaults, the
// five-layer load-and-merge pipeline (defaults -> user file -> project file -> environment ->
// flags), per-leaf fallback-not-crash validation, provenance tracking, and JSON Schema emission.
//
// Every leaf in Config carries up to five struct tags — json, doc, rng, enum, sec — read by
// schema.go and by the docs generator. config's own allow-set (00-ARCHITECTURE.md §3.2) is
// {core}: it may not import internal/paths, internal/logging or internal/obs, which is why Load
// joins the two config-file locations inline with filepath.Join rather than through
// paths.Global/paths.Of, and never logs or writes a file itself — reporting a bad value is the
// composition root's job, once it has called Load (see Load's doc comment and
// ViolationsFromWarnings).
//
// Behaviour on invalid configuration is never "crash": Load falls every violating leaf back to
// its default and reports the problem through the returned []Warning, so a hook that reads bad
// config still runs (00-ARCHITECTURE.md §11.3).
package config
