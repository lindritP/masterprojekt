# 0008. Give each variant its own Go module

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS DR-016, Appendix A; Research-Design MR-04

## Context

DR-016 forbids V0 and V1 from sharing any Go package, because shared code would show up in
both migration diffs and contaminate both effort figures. A rule that only lives in review is
easy to break by accident.

## Decision

`app/v0` and `app/v1` are separate Go modules, tied together for local development by
`go.work`. Module paths are `github.com/lindritP/masterprojekt/app/ccsra-v0` and
`.../app/ccsra-v1`. The paths do not end in `/v0` and `/v1` because Go reads a trailing
`/vN` as a major-version suffix and rejects `/v0`.

Shared, non-Go assets (`api/`, `db/migrations/`, `test/`) stay outside both modules (DR-017).
Build tooling lives in its own module under `tools/`.

## Consequences

- An import from one variant into the other fails to compile unless someone adds an explicit
  `require`, which is visible in review.
- Each variant has its own `go.sum`, so dependency changes are attributable per variant.
- Neither module can be fetched remotely with `go install`; nothing needs that.
