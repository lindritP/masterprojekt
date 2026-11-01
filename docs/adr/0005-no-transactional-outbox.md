# 0005. Publish events after commit, without a transactional outbox

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS §5.6, FR-208, FR-304, FR-023

## Context

`case-service` publishes after its database commit and `intake-service` after confirming the
object. A crash between the two loses the event. A transactional outbox would fix this, but it
is a provider-neutral reliability layer that would make the publish side look equally portable
on every broker, hiding part of the CP-4 coupling the study exists to measure.

## Decision

No transactional outbox. Events are published after commit; delivery is at-least-once once
published.

## Consequences

- A lost `document.uploaded` leaves a case in `VALIDATING` indefinitely.
- A reconciliation job that re-publishes for documents stuck in `UPLOADED` is `[OPEN]` for M2,
  to be added only if load runs show the problem in practice.
- Publish-side break points stay visible and are attributed as `CP-4-publish`.
