// Package config implements Qompack's Appendix C configuration schema: typed defaults, the
// five-layer load-and-merge pipeline (defaults -> user file -> project file -> environment ->
// flags), per-leaf fallback-not-crash validation, provenance tracking, and JSON Schema emission.
//
// Every leaf in Config carries up to five struct tags — json, doc, rng, enum, sec — read by
// schema.go and by the docs generator. config's own allow-set (00-ARCHITECTURE.md §3.2) is
// {core, paths} since owner decision D22: the loaders name the two config-file locations through
// paths.Global and paths.Of and read them with delete sharing (paths.ReadFileShared,
// paths.OpenSharedLeaf). It may not import internal/logging (which imports config) or internal/obs,
// so it never logs, and it never writes a file — reporting a bad value is the composition root's
// job, once it has called Load (see Load's doc comment and ViolationsFromWarnings).
//
// Behaviour on invalid configuration is never "crash": Load falls every violating leaf back to
// its default and reports the problem through the returned []Warning, so a hook that reads bad
// config still runs (00-ARCHITECTURE.md §11.3).
package config
