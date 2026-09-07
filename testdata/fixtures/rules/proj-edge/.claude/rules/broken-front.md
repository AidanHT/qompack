---
paths: ["crlf/**"]
description: the fence below is missing on purpose

# Broken frontmatter

This block is opened and never closed, so Parse reports Present=false and the whole file degrades
to an unscoped rule the host re-injects itself. PathScoped must never return it.
