# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `devtool bundle` assembles a deterministic, versioned plugin bundle per release target, with a
  `BUNDLE.json` identity and a `sha256sum`-format `checksums.txt`; see `packaging/README.md`.
