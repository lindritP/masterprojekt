# 0001. Record architecture decisions

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS NFR-003, Research-Design §6 (reliability)

## Context

NFR-003 requires every design decision that affects a measurable quantity to be recorded.
The study has a single researcher, so the decision trail is also the main defence against
the reliability threat in Research-Design §6.

## Decision

Decisions are recorded as numbered Markdown files in `docs/adr/`, using `0000-template.md`.
Numbers are never reused. A decision that changes is not edited: a new ADR supersedes it,
and the old one's status is set to `Superseded by NNNN`.

An ADR starts as `Proposed` when it needs a supervisor's or other stakeholder's agreement,
and becomes `Accepted` once agreed.

## Consequences

Every ADR that affects a measured quantity says so under *Consequences*, so the thesis can
cite the decision next to the number it influenced.
