# 0012. Validate on upload; submission settles results already in

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS §3.2 (T2–T5), FR-205, FR-207, §5.6; Research-Design MR-02 (S3), MR-03

## Context

`intake-service` publishes `document.uploaded` when an upload completes (FR-304), and the worker
validates on that event. Citizens usually upload first and submit later, so most validation
results arrive while the case is still in `DRAFT` or `NEEDS_INFO`. SRS §3.2 only describes
results arriving in `VALIDATING`.

## Decision

The domain stores a result as soon as it arrives. The document keeps its status until the case
is submitted. On submit (T2/T3), documents that already have a result go straight to `VALID`
or `INVALID`; the rest go to `VALIDATING`. If nothing is outstanding, T4 or T5 follows in the
same operation, which then writes two state-change audit entries (INV-3: one per state change).

Two further readings of the SRS, made explicit by the domain tests:

- Documents still in `PENDING_UPLOAD` were never submitted. They do not block or count towards
  T4/T5.
- In `DRAFT`, attaching a second document with an active document's filename is refused
  (`VALIDATION_FAILED`). Only in `NEEDS_INFO` does it supersede the old one (FR-211, INV-7).

## Consequences

- The measured "submit → validated" latency (scenario S3) is often zero, because validation
  already happened. S3 must therefore measure **upload → `document.validated`** (MR-03 already
  defines it that way) and must not be read as a submit latency.
- `CaseRepository.Transition` takes a function returning a list of audit entries, not a single
  entry, and `RecordValidationResult` runs the domain operation inside the repository
  transaction (SRS §7.2).
