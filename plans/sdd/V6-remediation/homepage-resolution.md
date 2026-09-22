# Plugin homepage correction

2026-09-22. The plugin generator and committed manifest named
`https://github.com/qompack/qompack`. The repository's configured origin is
`https://github.com/AidanHT/qompack`; public GitHub API lookups returned 404 for
the former and 200 for the latter. The homepage now uses the public origin.
The Go module identity is unchanged.

[Lookup responses and timestamps](public-name-lookups-20260922/results.json)
preserve the observations. Public npm, PyPI and crates exact-name endpoints
returned 404; the fetched official Claude marketplace had no exact `qompack`
entry. These observations do not reserve a name or prove registrability,
trademark clearance, private namespace ownership or future availability.

The first package test run, `plugin-homepage-manifest`, failed the historical
golden assertion. The golden remains unchanged. Its current assertion maps
exactly one obsolete homepage field to the corrected value and compares every
remaining byte. `plugin-homepage-historical-mapping` passes the package, including
the committed-manifest comparison. This is metadata/source evidence, not an
installed-host or release-package result.
