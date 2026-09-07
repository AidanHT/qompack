---
paths: ["src/webhooks/*.ts"]
description: Webhook delivery rules
---
# Webhook rules

Deliveries are retried with jittered backoff and deduplicated on delivery id.
