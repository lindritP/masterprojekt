# 0004. Build V1 before V0

- **Status:** Proposed — to be confirmed with the supervisors
- **Date:** 2026-11-01
- **Affects:** SRS §11 (B0–B6), DR-014, DR-016; Research-Design §8 (P2, P3)

## Context

SRS §11 builds the abstracted variant V1 first, against the local stack, and derives V0
afterwards. Research-Design §8 still lists V0 in P2 and V1 in P3. The two documents disagree.

V1 runs entirely on the local stack (DR-014), so it can be built before any cloud budget is
approved (Research-Design D-1, RSK-1). V0 needs a live AWS account from its first line.

## Decision

Build V1 first (B0–B4, no cloud), then V0 (B5). Research-Design §8 is to be aligned once the
supervisors agree.

## Consequences

- Budget approval is off the critical path for the first four build stages.
- Risk: V0 could end up as "V1 with the interfaces deleted" rather than natively written code,
  which would weaken the RQ3 comparison. Mitigations, all from SRS §11:
  V0 lives in its own Go module and shares no package with V1 (DR-016, ADR 0008);
  it is written from the AWS SDK documentation, not by editing V1;
  it is reviewed against CR-011/CR-012 and the review is recorded.
