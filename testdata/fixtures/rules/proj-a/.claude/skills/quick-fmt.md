---
name: quick-fmt
description: Format this file.
---
Run the project formatter over the file currently open, then stop. Do not
reorder imports beyond what the formatter does on its own, do not rename
anything, and do not touch a second file: the whole point of this skill is that
its diff is reviewable at a glance.

If the formatter is not configured for this repository, say so and stop rather
than guessing at one.
