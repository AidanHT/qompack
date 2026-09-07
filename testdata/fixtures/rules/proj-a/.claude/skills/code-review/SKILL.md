---
name: code-review
description: Review the current diff for correctness bugs.
---
Read the staged diff and report correctness bugs only: off-by-one errors, nil
dereferences, unchecked error returns, and lock/unlock mismatches.

Ignore formatting and naming; a separate skill owns those.
