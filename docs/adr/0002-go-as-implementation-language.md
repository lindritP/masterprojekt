# 0002. Use Go 1.26 as the implementation language

- **Status:** Accepted
- **Date:** 2026-09-25 (version pinned 2026-11-01)
- **Affects:** SRS §2.5, DR-001, DR-002, NFR-011; Research-Design D-2

## Context

All four services must build to small static binaries for `linux/amd64` and `linux/arm64`
(DR-001, DR-002), and every provider on the shortlist must have a maintained SDK in the
chosen language.

## Decision

Go, pinned to **1.26** (`go 1.26.3` in every `go.mod` and in `go.work`). The minor version
is only raised by a new ADR.

## Consequences

- Static binaries in `distroless`/`scratch` images meet DR-001 and NFR-011 without effort.
- The AWS SDK for Go v2 covers every baseline service; EU providers are reached through
  S3-compatible, AMQP and OIDC clients, which Go has mature libraries for.
- A toolchain change mid-study could move performance numbers, so it is frozen.
