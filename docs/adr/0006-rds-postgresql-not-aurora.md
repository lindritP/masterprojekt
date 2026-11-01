# 0006. Use RDS for PostgreSQL, not Aurora

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS CR-005, FR-033, A-2; Research-Design §2.2 (CP-3)

## Context

On AWS, an AWS-optimised team could choose Aurora PostgreSQL, which would show stronger
CP-3 coupling (proprietary storage engine, Aurora-only features). But Aurora is a
PostgreSQL-*compatible* engine, not PostgreSQL.

## Decision

V0 uses Amazon RDS for PostgreSQL 16. CP-3 coupling is placed in the control plane: IAM
database authentication, point-in-time restore, Multi-AZ, VPC-private connectivity and
parameter groups (CR-005).

## Consequences

- Assumption A-2 (PostgreSQL 16 on every target) holds, and the schema stays vanilla (FR-033).
- The CP-3 finding is purely about the operational plane; an engine change is not mixed in.
- An examiner may call this choice conservative. The answer: Aurora would confound the
  data-plane/control-plane separation that CP-3 is designed to measure.
