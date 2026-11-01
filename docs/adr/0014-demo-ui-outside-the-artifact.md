# 0014. Keep the demo UI outside the measured artifact

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS §2.2, DR-016; Research-Design MR-04, §9

## Context

SRS §2.2 states that no UI is built: the API is the product surface and the subject of every
measurement. For presentations, a clickable interface is still needed to show the case
workflow from both sides — citizen and caseworker.

A UI deployed to the cloud targets would add infrastructure (static hosting, CDN, its own
OIDC client) to each migration and to the effort figures, without answering any research
question.

## Decision

A demo web UI lives in `demo/web/` (React, TypeScript, Vite, `oidc-client-ts`), outside
`app/v0` and `app/v1`. It runs only locally (`make demo`, port 3000), uses the public API like
any other client, and logs in through the same `ccsra-web` OIDC client as the API explorer.

It is never deployed to a cloud target, no requirement traces to it, and no metric counts it.

## Consequences

- Migration effort (`ME_code`, `ME_iac`, `ME_cfg`) is computed within the application trees and
  the target IaC, so the UI cannot contaminate it.
- The UI needed no API change. It relies on two local-only settings: the gateway's
  `CCSRA_CORS_ALLOWED_ORIGINS` and the web origins of the `ccsra-web` client in the local realm.
- The UI is German and follows the ubiquitous language of SRS §3.4; code stays English.
- CI only type-checks and builds it, so it does not rot unnoticed.
