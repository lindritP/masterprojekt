# 0011. Apply migrations as a one-shot step with golang-migrate

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS FR-213 (closes its `[OPEN]`), FR-030, FR-033, §5.7, DR-012

## Context

FR-213 left open whether migrations run at service startup or in an init container. Running
them at startup would give `case-service` DDL rights at runtime, which conflicts with FR-030:
the application role must not be able to rewrite `audit_events`.

## Decision

Migrations are plain, forward-only SQL files in `db/migrations/`, applied by
`migrate/migrate` (golang-migrate, pinned image) as a one-shot step before `case-service`
starts. Locally that is the `db-migrate` service run by `make stack-up`; on Kubernetes it is an
init step with the same image.

The migration role owns the schema. The application connects as `ccsra_app`, which only gets
the privileges it needs; on `audit_events` that is `SELECT` and `INSERT`. Every environment
must provide `ccsra_app` (locally: `deploy/local/postgres/init.sql`; on AWS: OpenTofu, with
the `rds_iam` grant for IAM authentication).

## Consequences

- The application role cannot run DDL and cannot update or delete audit rows. The
  conformance test `AuditLog_IsAppendOnly` checks this against the real database.
- The services contain no migration code, so the V0/V1 migration diffs do not include it.
- Creating `ccsra_app` is a per-target IaC step and is counted in `ME_iac`.
