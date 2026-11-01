# 0003. Use AWS as the hyperscaler baseline

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS §2.5, §6.2, §7.3; Research-Design §2.2, §2.6, §5.1; closes D-8, D-10

## Context

Draft v0.2 used Azure as the baseline. The author prefers AWS. The choice decides where each
coupling point sits on the interface-standardisation axis, and therefore which coupling point
carries the Art. 30(2)–(3) argument.

## Decision

The baseline is AWS in region `eu-central-1`: Amazon EKS (CP-1), S3 (CP-2), RDS for
PostgreSQL (CP-3), SQS FIFO (CP-4) and Cognito user pools (CP-5). The full rationale is in
Research-Design §2.6.

## Consequences

- CP-2 becomes the de-facto-standard case and Art. 30(2)–(3) test case #1. CP-4 becomes the
  proprietary-interface case, so the AMQP "open but not equivalent" argument is lost.
- The portable S3 adapter already runs against Amazon S3, so the V0/V1 gap at CP-2 on the
  baseline is expected to be small. This is reported, not engineered away.
- New baseline costs to budget: EKS control-plane fee, Cognito client-credentials token fees
  (Research-Design RSK-5, RSK-11, RSK-12).
- The v0.2 Azure mapping is kept in the revision history as a counterfactual for the
  external-validity discussion.
