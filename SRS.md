# Software Requirements Specification

**Citizen Case Submission Reference Application (CCSRA)**

| | |
|---|---|
| **Document** | Software Requirements Specification |
| **Version** | 0.3 |
| **Date** | 2026-11-01 |
| **Author** | Lindrit Prekaj |
| **System** | CCSRA — Citizen Case Submission Reference Application |
| **Language / runtime** | Go |
| **Standard** | Structure adapted from ISO/IEC/IEEE 29148:2018 |
| **Companion** | [`Research-Design.md`](./Research-Design.md) — research questions, method, provider selection, evaluation model |
| **Status** | Open items marked `[OPEN]` |

---

## Revision History

| Version | Date | Change |
|---|---|---|
| 0.1 | 2026-09-20 | Initial combined draft (application + research). |
| 0.2 | 2026-09-25 | Split: research content moved to `Research-Design.md`. This document is now a build-ready application specification. Language fixed to Go. Feature scope extended with the NEEDS_INFO resubmission loop and the caseworker role. Interface contracts, data model, event schemas, error catalogue and acceptance criteria added. |
| 0.3 | 2026-11-01 | Hyperscaler baseline switched from Azure to **AWS**. V0 native bindings (§6.2), adapter matrix (§7.3), configuration contract (§9.3) and repository layout rewritten for EKS, S3, RDS for PostgreSQL, SQS FIFO and Cognito. Upload contract generalised from presigned PUT to **presigned upload** (PUT or POST form), because an S3 presigned PUT cannot enforce a maximum size (FR-301, FR-302, §7.2). Lease semantics in FR-407 made provider-neutral. Stage B0 set up: Go pinned to 1.26, local S3 store changed from MinIO (archived) to SeaweedFS (DR-012), register schema referenced in §6.1, Appendix A extended with tooling. |

---

# 1. Introduction

## 1.1 Purpose

This document specifies **what to build**: the CCSRA reference application, its two implementation variants, its interfaces, its data, and the tests that define "done". It is written to be sufficient for implementation without reference to any other document.

## 1.2 Context

CCSRA is the software artifact of a Master's thesis on cloud portability under Article 30 of the EU Data Act. Its purpose is to serve as a **measurable specimen**: an application that deliberately embodies five distinct classes of dependency on provider-managed cloud services, so that the effort and the functionality loss involved in moving it between providers can be measured.

Two consequences follow, and they govern every requirement in this document:

1. **Some requirements exist for measurement, not for product value.** They are marked with a `▶ Measurement rationale` note. Do not "improve" them away.
2. **Two variants of the same application are built.** **V0** is written the way a team optimizing for a single cloud provider would write it. **V1** is functionally identical but built behind provider-agnostic ports. Both are deployed and measured.

The research questions, the justification for the five coupling points, the provider selection procedure and the evaluation model are **not** in this document. See [`Research-Design.md`](./Research-Design.md).

## 1.3 Audience

| Reader | Reads |
|---|---|
| Implementer (the author) | All sections |
| Thesis supervisor reviewing scope | §2, §3, §11, §12 |
| A replicating researcher | §5 (contracts), §9 (deployment), §10 (tests) |

## 1.4 Definitions

| Term | Definition |
|---|---|
| **Case** | A citizen's submission to the authority (German: *Vorgang* / *Antrag*). The aggregate root. |
| **Document** | A file a citizen attaches to a case. |
| **Coupling Point (CP)** | A location where the system depends on a capability supplied by a specific cloud provider. |
| **V0 / native variant** | Implementation using provider SDKs directly, with no indirection. |
| **V1 / abstracted variant** | Functionally identical implementation using hexagonal ports and per-target adapters. |
| **Port** | A Go interface expressing a capability in domain vocabulary. |
| **Adapter** | An implementation of a port against one concrete provider. |
| **Target** | A deployment environment (AWS baseline, or an EU provider). |
| **Subject** | An authenticated principal, derived from a token. Never persisted as a user record. |
| **Presigned URL** | A time-limited, signed URL permitting direct object upload or download without the application handling the bytes. |
| **Presigned upload** | A presigned URL for upload, issued either as a signed `PUT` request or as a signed `POST` form with policy fields. Which form is used is chosen by the adapter and returned to the client. |
| **Break point** | A discrete incompatibility encountered when porting to a target. Logged, not fixed silently. |
| **Conformance suite** | A test suite that must pass unchanged across variants and targets. |

## 1.5 Requirement Conventions

- **SHALL** = mandatory. **SHOULD** = recommended. **MAY** = optional.
- Priority: **M** (Must), **S** (Should), **C** (Could).
- Identifier scheme:

| Prefix | Domain |
|---|---|
| `FR-0xx` | Cross-cutting functional |
| `FR-1xx` | `identity-gateway` |
| `FR-2xx` | `case-service` |
| `FR-3xx` | `intake-service` |
| `FR-4xx` | `validation-worker` |
| `CR-xxx` | Coupling requirements (V0 native bindings) |
| `AR-xxx` | Abstraction requirements (V1 ports and adapters) |
| `NFR-xxx` | Non-functional |
| `DR-xxx` | Deployment and configuration |
| `AC-xxx` | Acceptance criteria |

---

# 2. Overall Description

## 2.1 System Context

```
                    ┌─────────────┐        ┌──────────────────┐
   Citizen  ───────▶│             │───────▶│  Identity        │
                    │  identity-  │  OIDC  │  Provider (CP-5) │
   Caseworker ─────▶│  gateway    │        └──────────────────┘
                    │             │
   k6 harness ─────▶│  (public)   │
                    └──────┬──────┘
                           │ internal HTTP
              ┌────────────┴────────────┐
              ▼                         ▼
     ┌─────────────────┐       ┌──────────────────┐      ┌──────────────────┐
     │  case-service   │──────▶│  PostgreSQL      │      │  Object Storage  │
     │                 │       │  (CP-3)          │      │  (CP-2)          │
     └────────┬────────┘       └──────────────────┘      └────────▲─────────┘
              │                                                    │ SDK
              │                 ┌──────────────────┐               │
              └── publish ─────▶│  Message Broker  │      ┌────────┴─────────┐
                                │  (CP-4)          │◀─────│  intake-service  │
                                └────────┬─────────┘      └──────────────────┘
                                         │ consume                  ▲
                                         ▼                          │ presign
                                ┌──────────────────┐                │ request
                                │ validation-worker│────────────────┘
                                └──────────────────┘
                                         │
                            direct HTTPS │ (presigned URL, no SDK)
                                         ▼
                                  Object Storage
```

All four services run as containers on Kubernetes (**CP-1**).

## 2.2 Actors

| Actor | Authentication | Capabilities |
|---|---|---|
| **Citizen** | OIDC Authorization Code + PKCE; role `citizen` | Create own cases, attach/remove documents, submit, view own cases and their audit trail |
| **Caseworker** | Same flow; role `caseworker` | List and filter all cases, view any case with validation results, decide cases in `UNDER_REVIEW` |
| **Harness** | OAuth 2.0 client credentials; role `citizen` or `caseworker` | Same as the role it holds. Used by k6 for load generation. |
| **Operator** | Out of band (kubectl, provider console) | Deploy, observe, tear down. No application role. |

> There is no human end user. No usability, accessibility, i18n or front-end requirements are in scope. No UI is built; the API is the product surface.

## 2.3 Service Decomposition

| Service | Kind | Owns | Primary CP | Links a provider SDK for |
|---|---|---|---|---|
| `identity-gateway` | Public HTTP edge | Nothing persistent | **CP-5** Identity | Token validation / IdP |
| `case-service` | Internal HTTP | PostgreSQL schema | **CP-3** Relational data | Database |
| `intake-service` | Internal HTTP | Object storage bucket | **CP-2** Object storage | Object storage |
| `validation-worker` | Queue consumer, no business HTTP | Nothing persistent | **CP-4** Messaging (consume) | Message broker (consumer) |

## 2.4 Architecture Constraints

| ID | Constraint | Pri |
|---|---|---|
| `FR-001` | The system SHALL consist of exactly these four deployable services. No additional service SHALL be introduced without a corresponding update to this document. | M |
| `FR-002` | Services SHALL communicate only via: (a) internal HTTP/JSON, and (b) events on the message broker. No shared database, no shared file system, no direct service-to-service database access. | M |
| `FR-003` | The **high-coupling facet** of each primary coupling point SHALL be carried by exactly one service, as in §2.3. No other service SHALL link that provider's SDK for that capability. | M |
| `FR-004` | `validation-worker` SHALL read and write objects **exclusively via presigned URLs obtained from `intake-service`**, using plain HTTPS. It SHALL NOT link an object-storage SDK. | M |
| `FR-005` | Only `case-service` SHALL hold a database connection. | M |
| `FR-006` | V0 and V1 SHALL be functionally identical as observed through the public API. The E2E conformance suite (§10.2) SHALL pass unchanged against both variants on every target. | M |

> **▶ Measurement rationale for FR-003 / FR-004 / FR-005.** These are not architectural preferences. If two services linked the object-storage SDK, a break point found during migration could not be attributed to a single coupling class without ambiguity, and the per-CP effort figures would be confounded. FR-004 is the mechanism that lets `validation-worker` process document bytes while keeping CP-2 confined to `intake-service`.

> **Note on CP-4's two facets.** `intake-service` and `case-service` *publish* events; only `validation-worker` *consumes* them. Publish-side coupling is shallow (send a message). Consume-side coupling is where the provider-specific semantics live: ordered message groups, visibility timeout and its extension, receive count, dead-letter redrive. CP-4's high-coupling facet is therefore confined to `validation-worker`, and break points on the publish side are attributed separately as `CP-4-publish`.

## 2.5 Operating Environment

| Aspect | Value |
|---|---|
| Language | **Go 1.26** (pinned in every `go.mod` and `go.work`; ADR 0002) |
| Runtime | Linux containers, `distroless` or `scratch` base |
| Orchestration | Kubernetes, packaged with Helm |
| Infrastructure | OpenTofu |
| Database | PostgreSQL 16 `[OPEN: confirm availability across all targets at M1]` |
| Compute | Amazon EKS (baseline) / any CNCF-conformant managed Kubernetes (portable) |
| Relational database | Amazon RDS for PostgreSQL (baseline) / managed or self-hosted PostgreSQL (portable) |
| Object storage | Amazon S3 (baseline) / S3-compatible (portable) |
| Broker | Amazon SQS FIFO (baseline) / AMQP, RabbitMQ (portable) |
| Identity | Amazon Cognito user pools (baseline) / OIDC-compliant IdP, Keycloak (portable) |
| Baseline region | AWS `eu-central-1` (Frankfurt), held constant for the whole study |
| Observability | OpenTelemetry (traces, metrics), structured JSON logs to stdout |
| Load generator | k6 |

## 2.6 Assumptions and Dependencies

| ID | Assumption |
|---|---|
| `A-1` | Every target provides managed Kubernetes. Enforced as a hard provider-selection criterion. |
| `A-2` | Every target provides PostgreSQL 16 or later, either managed or self-hosted. |
| `A-3` | No real personal data is used anywhere. All citizens, caseworkers and documents are synthetic. |
| `A-4` | Environments are torn down between measurement campaigns and need not run continuously. |
| `A-5` | Byte-identical PDF rasterisation across CPU architectures is **not** assumed; determinism is asserted on a numeric pipeline instead (§4.5). |

---

# 3. Domain Model

## 3.1 Entities

### `Case` — aggregate root

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | UUIDv7 | PK | Time-sortable; enables cursor pagination |
| `reference` | string | unique, not null | Human-readable, `CASE-<year>-<seq>` |
| `caseType` | enum | not null | `BUILDING_PERMIT` \| `RESIDENCE_REGISTRATION` \| `BENEFIT_CLAIM` |
| `state` | enum | not null | See §3.2 |
| `subjectId` | string | not null, immutable | The `sub` claim of the owning citizen |
| `title` | string | ≤ 200 chars | Citizen-supplied |
| `version` | int | not null, ≥ 1 | Optimistic lock; increments on every mutation |
| `createdAt` | timestamptz | not null | |
| `updatedAt` | timestamptz | not null | |
| `submittedAt` | timestamptz | nullable | First transition into `VALIDATING` |
| `decidedAt` | timestamptz | nullable | |
| `decidedBy` | string | nullable | Caseworker `sub` |
| `decisionReason` | string | nullable, required on `REJECTED` | |

### `Document`

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | UUIDv7 | PK | |
| `caseId` | UUID | FK → `Case`, not null | |
| `filename` | string | ≤ 255 chars, not null | |
| `declaredMime` | string | not null | As claimed by the client |
| `detectedMime` | string | nullable | Determined by the worker |
| `sizeBytes` | int64 | nullable | Confirmed at upload completion |
| `sha256` | char(64) | nullable | Computed by the worker |
| `storageKey` | string | unique, not null, immutable | See §5.8 |
| `status` | enum | not null | `PENDING_UPLOAD` \| `UPLOADED` \| `VALIDATING` \| `VALID` \| `INVALID` \| `SUPERSEDED` \| `DELETED` |
| `supersedesId` | UUID | nullable, FK → `Document` | Set when re-uploaded after `NEEDS_INFO` |
| `createdAt` | timestamptz | not null | |
| `uploadedAt` | timestamptz | nullable | |
| `validatedAt` | timestamptz | nullable | |

### `ValidationResult`

| Field | Type | Constraints |
|---|---|---|
| `id` | UUIDv7 | PK |
| `documentId` | UUID | FK, unique — write-once |
| `outcome` | enum | `PASS` \| `FAIL` |
| `failureCodes` | string[] | empty on `PASS` |
| `checks` | JSONB | `{detectedMime, sizeBytes, sha256, pageCount, contentDigest, thumbnailKey}` |
| `workerVersion` | string | Image tag or build SHA |
| `durationMs` | int | |
| `createdAt` | timestamptz | |

### `AuditEvent` — append-only

| Field | Type | Notes |
|---|---|---|
| `id` | bigserial PK | |
| `caseId` | UUID FK | |
| `occurredAt` | timestamptz | |
| `actorType` | enum | `CITIZEN` \| `CASEWORKER` \| `SYSTEM` |
| `actorId` | string | `sub`, or service name for `SYSTEM` |
| `eventType` | string | e.g. `case.created`, `case.state_changed`, `document.attached` |
| `correlationId` | string | Links the entry to the originating request |
| `payload` | JSONB | Event-specific detail |

### `ProcessedMessage` — consumer idempotency

| Field | Type | Notes |
|---|---|---|
| `messageId` | string | Broker message id, or the CloudEvent `id`. Composite PK with `consumer`. |
| `consumer` | string | Logical consumer name. Composite PK with `messageId`. |
| `processedAt` | timestamptz | |
| `resultRef` | string nullable | Id of what was produced, for replaying the same response |

> Owned by `case-service`, not by `validation-worker`. The worker stays database-free (FR-005); idempotency is enforced at the owner of the state, via an `Idempotency-Key` header (§5.1).

## 3.2 Case State Machine

Six states. `ACCEPTED` and `REJECTED` are terminal.

```
                       ┌──────────────── resubmit ◀────────────┐
                       ▼                                       │
  (create) ──▶ DRAFT ──submit──▶ VALIDATING ──any FAIL──▶ NEEDS_INFO
                                     │
                                 all PASS
                                     │
                                     ▼
                              UNDER_REVIEW ──accept──▶ ACCEPTED ▣
                                     └──────reject──▶ REJECTED ▣
```

| # | From | Trigger | Guard | To |
|---|---|---|---|---|
| `T1` | — | `POST /cases` | `caseType` valid | `DRAFT` |
| `T2` | `DRAFT` | `POST /cases/{id}/submit` | ≥ 1 document in `UPLOADED` | `VALIDATING` |
| `T3` | `NEEDS_INFO` | `POST /cases/{id}/submit` | ≥ 1 document in `UPLOADED` created after `decidedAt`/last transition | `VALIDATING` |
| `T4` | `VALIDATING` | last outstanding `document.validated` received | all non-superseded documents `VALID` | `UNDER_REVIEW` |
| `T5` | `VALIDATING` | last outstanding `document.validated` received | ≥ 1 non-superseded document `INVALID` | `NEEDS_INFO` |
| `T6` | `UNDER_REVIEW` | `POST /worker/cases/{id}/decision` `ACCEPTED` | actor has role `caseworker` | `ACCEPTED` |
| `T7` | `UNDER_REVIEW` | `POST /worker/cases/{id}/decision` `REJECTED` | role `caseworker`, `reason` non-empty | `REJECTED` |

Any transition not listed is invalid and SHALL be rejected with `INVALID_STATE_TRANSITION` (HTTP 409).

## 3.3 Invariants

| ID | Invariant |
|---|---|
| `INV-1` | A case in a terminal state never transitions again. |
| `INV-2` | Documents may be attached or deleted only while the case is in `DRAFT` or `NEEDS_INFO`. |
| `INV-3` | Every state change writes exactly one `AuditEvent` **in the same database transaction** as the state change. |
| `INV-4` | `Document.storageKey` is immutable once assigned. |
| `INV-5` | `Case.subjectId` is immutable. |
| `INV-6` | Every mutation increments `Case.version`. A mutation carrying a stale version fails with `CONCURRENT_MODIFICATION` (HTTP 409). |
| `INV-7` | At most one non-`SUPERSEDED` document exists per `(caseId, filename)`. |
| `INV-8` | `ValidationResult` is written at most once per document. A second write for the same document is accepted idempotently only if byte-identical; otherwise it fails. |
| `INV-9` | A citizen can read or mutate only cases whose `subjectId` equals their own `sub`. A caseworker can read all cases and decide only cases in `UNDER_REVIEW`. |

## 3.4 Ubiquitous Language (DE ↔ EN)

| English (code) | Deutsch (Thesis) | Note |
|---|---|---|
| Case | Vorgang / Antrag | Aggregate root. Code uses `Case` throughout. |
| Case reference | Aktenzeichen | |
| Document | Dokument / Nachweis | |
| Submit | Einreichen | |
| Validation | Prüfung | Automated, technical. Not a legal assessment. |
| Needs info | Nachforderung | Citizen must supply corrected documents |
| Under review | In Bearbeitung | Awaiting a caseworker decision |
| Decision | Bescheid | |
| Caseworker | Sachbearbeiter:in | |
| Audit trail | Vorgangshistorie | |

> Code, API, database and events are **English only**. German terms appear only in the thesis text. This table is the normative mapping.

---

# 4. Functional Requirements

## 4.1 Cross-Cutting

| ID | Requirement | Pri |
|---|---|---|
| `FR-010` | Every service SHALL expose `GET /healthz` (liveness, no dependency checks) and `GET /readyz` (readiness, checks its own dependencies). | M |
| `FR-011` | Every service SHALL expose `GET /metrics` in Prometheus exposition format. | M |
| `FR-012` | Every service SHALL emit structured JSON logs to stdout, one object per line, including `correlationId`, `service`, `level`, `msg`. | M |
| `FR-013` | Every service SHALL emit OpenTelemetry traces and propagate W3C `traceparent` across HTTP calls and events. | S |
| `FR-014` | Every inbound request SHALL carry or be assigned an `X-Correlation-Id`, which SHALL be echoed in the response, propagated to every downstream call, and included in every emitted event and audit entry. | M |
| `FR-015` | Every service SHALL read all configuration from environment variables only (§9.3). No configuration file, no runtime configuration API. | M |
| `FR-016` | Every service SHALL shut down gracefully on `SIGTERM`: stop accepting new work, finish in-flight work up to a configured drain timeout, then exit 0. | M |
| `FR-017` | All errors returned over HTTP SHALL use the format and codes in §5.2. | M |

## 4.2 `identity-gateway` — CP-5 carrier

| ID | Requirement | Pri |
|---|---|---|
| `FR-100` | SHALL be the only service exposed outside the cluster. All public API traffic SHALL pass through it. | M |
| `FR-101` | SHALL authenticate callers by validating a Bearer JWT: signature against the IdP's JWKS, `iss`, `aud`, `exp`, `nbf`. Invalid or missing token → `401 UNAUTHENTICATED`. | M |
| `FR-102` | SHALL cache the IdP's JWKS with a configurable TTL and refresh it on unknown `kid`, with a rate limit on refreshes. | M |
| `FR-103` | SHALL map provider-specific token claims onto an internal `Subject` (`subjectId`, `roles[]`, `authLevel`) and propagate **only** the internal representation downstream, as `X-Subject-Id`, `X-Subject-Roles`, `X-Auth-Level`. The raw token SHALL NOT be forwarded. | M |
| `FR-104` | SHALL enforce coarse-grained role authorization at the edge: `/api/v1/worker/**` requires role `caseworker`; all other `/api/v1/**` requires role `citizen` or `caseworker`. Failure → `403 FORBIDDEN_ROLE`. | M |
| `FR-105` | SHALL route requests to `case-service` and `intake-service` per §5.3, with a configurable per-call timeout, bounded retries on idempotent methods only, and a circuit breaker. | M |
| `FR-106` | SHALL support OAuth 2.0 client-credentials tokens for the harness, distinguishable from citizen tokens, and SHALL apply the same role mapping. | M |
| `FR-107` | SHALL apply per-subject rate limiting with a configurable limit; exceeded → `429 RATE_LIMITED` with `Retry-After`. | S |
| `FR-108` | SHALL reject request bodies larger than a configured maximum with `413 PAYLOAD_TOO_LARGE`. | M |

> **▶ Measurement rationale for FR-103.** Claim mapping is where identity coupling concentrates. Confining it to one function in one service makes the CP-5 migration diff a small, precisely locatable region of code — which is exactly what makes the effort figure meaningful rather than diffuse.

## 4.3 `case-service` — CP-3 carrier

| ID | Requirement | Pri |
|---|---|---|
| `FR-200` | SHALL own the PostgreSQL schema in §5.7 and be the only database client. | M |
| `FR-201` | SHALL create a case in `DRAFT` with a generated `id` and a unique, monotonically allocated `reference`. | M |
| `FR-202` | SHALL enforce the state machine of §3.2. Invalid transitions → `409 INVALID_STATE_TRANSITION`. | M |
| `FR-203` | SHALL enforce optimistic concurrency via `Case.version`, exposed as an `ETag` and required as `If-Match` on all state-changing operations. Mismatch → `409 CONCURRENT_MODIFICATION`. | M |
| `FR-204` | SHALL write exactly one `AuditEvent` per state change, in the same transaction (INV-3). | M |
| `FR-205` | SHALL record document metadata on behalf of `intake-service` and own the `Document.status` lifecycle: `PENDING_UPLOAD` at registration → `UPLOADED` on completion → `VALIDATING` on case submit → `VALID` / `INVALID` on result, or `SUPERSEDED` / `DELETED`. No other service SHALL write this field. | M |
| `FR-206` | SHALL record `ValidationResult` on behalf of `validation-worker`, idempotently, keyed by `Idempotency-Key` (INV-8). | M |
| `FR-207` | SHALL evaluate transitions `T4`/`T5` when the last outstanding validation result for a case arrives, atomically with recording that result. | M |
| `FR-208` | SHALL publish `case.submitted` and `case.decided` events after a successful commit. | M |
| `FR-209` | SHALL enforce INV-9 authorization on every read and write, using the propagated `Subject`, and SHALL NOT trust any client-supplied `subjectId`. | M |
| `FR-210` | SHALL support cursor-paginated listing of a citizen's own cases, and of all cases with state and type filters for caseworkers. | M |
| `FR-211` | SHALL mark a prior document `SUPERSEDED` when a new document with the same `filename` is attached to a case in `NEEDS_INFO` (INV-7). | M |
| `FR-212` | SHALL expose the audit trail of a case, subject to INV-9. | M |
| `FR-213` | SHALL apply all schema changes through versioned, forward-only migrations executed at startup or by an init container `[OPEN: choose at M0]`. | M |
| `FR-214` | SHALL provide a bulk export of all case data as JSON Lines, paginated, caseworker-only. | S |

> **▶ Measurement rationale for FR-214.** Not a product feature. Data Act Art. 23/25 require exportable data and digital assets; the artifact should be able to demonstrate its own exportability rather than merely discuss it.

## 4.4 `intake-service` — CP-2 carrier

| ID | Requirement | Pri |
|---|---|---|
| `FR-300` | SHALL own the object-storage bucket and be the only service linking an object-storage SDK. | M |
| `FR-301` | SHALL issue time-limited **presigned uploads** for documents, as a signed `PUT` URL or a signed `POST` form, and SHALL return the method, URL, required headers and form fields to the client. Document bytes SHALL NOT pass through any service. | M |
| `FR-302` | Presigned uploads SHALL constrain `Content-Type` and maximum size such that the **storage service itself** rejects a violating upload, and SHALL expire after a configurable TTL (default 15 min). | M |
| `FR-303` | Before issuing an upload slot, SHALL register the document with `case-service` in `PENDING_UPLOAD` and obtain its `documentId` and `storageKey`. | M |
| `FR-304` | On upload completion SHALL verify the object exists and its size matches, then mark the document `UPLOADED` via `case-service` and publish `document.uploaded`. | M |
| `FR-305` | SHALL reject completion if the object is absent (`409 UPLOAD_NOT_COMPLETED`) or its size exceeds the declared maximum (`413 PAYLOAD_TOO_LARGE`). | M |
| `FR-306` | SHALL issue time-limited **presigned GET URLs** for download, only after authorization has been verified with `case-service`. | M |
| `FR-307` | SHALL issue short-lived presigned downloads and uploads to `validation-worker` over the internal API, scoped to a single object key (FR-004). | M |
| `FR-308` | SHALL delete the object and mark the document deleted when a citizen removes a document from a case in `DRAFT` or `NEEDS_INFO`. Deletion SHALL be idempotent. | M |
| `FR-309` | SHALL apply a retention policy (write-once until `uploadedAt + retentionDays`) where the target supports it. Where unsupported, it SHALL log a structured `capability_unsupported` record and continue. It SHALL NOT silently skip. | S |
| `FR-310` | SHALL apply a lifecycle rule transitioning objects to a colder tier after a configured age, where supported; same unsupported handling as FR-309. | C |

> **▶ Measurement rationale for FR-301/302.** Presigned URL generation is deliberately chosen over upload-through-the-application. It is the object-storage mechanism with the **weakest** cross-provider equivalence — signature algorithms, header canonicalisation, expiry semantics and CORS all differ. Uploading through the app would have hidden CP-2 almost entirely.
>
> **▶ Why "PUT or POST" rather than PUT only.** An S3 presigned `PUT` can bind `Content-Type` and an *exact* `Content-Length`, but it cannot express a *maximum* size. The S3-native mechanism for a size ceiling is a presigned `POST` whose policy carries a `content-length-range` condition. Fixing the contract to `PUT` would have forced V0 into a non-native workaround on its own baseline. Which form an adapter uses, and whether a target's S3 implementation honours POST policies at all, is recorded per target — it is one of the places where an S3-compatible API turns out to be only partially equivalent.
>
> **▶ Measurement rationale for FR-309.** The requirement to *log rather than skip* is what converts an absent capability into a recorded observation. Silent degradation is the single easiest way to accidentally overstate portability.

## 4.5 `validation-worker` — CP-4 carrier

| ID | Requirement | Pri |
|---|---|---|
| `FR-400` | SHALL consume `document.uploaded` events from the broker. It SHALL expose no business HTTP endpoints — only `/healthz`, `/readyz`, `/metrics`. | M |
| `FR-401` | SHALL obtain a presigned GET URL from `intake-service` and download the object over plain HTTPS (FR-004). | M |
| `FR-402` | SHALL execute the validation pipeline of §4.5.1. | M |
| `FR-403` | SHALL be idempotent: reprocessing the same message SHALL NOT change the recorded outcome (INV-8), enforced via `Idempotency-Key` on the call to `case-service`. | M |
| `FR-404` | SHALL report the result to `case-service` and publish `document.validated`. | M |
| `FR-405` | SHALL process messages carrying the same `caseId` partition key in publish order. | M |
| `FR-406` | SHALL distinguish **transient** failures (retry with backoff) from **permanent** failures (dead-letter immediately). Messages exceeding the configured delivery count SHALL be dead-lettered. | M |
| `FR-407` | SHALL extend the message lease (lock or visibility timeout) for messages whose processing exceeds the lease duration, or SHALL be configured such that the lease duration exceeds the worst-case processing time. | M |
| `FR-408` | SHALL bound concurrency with a configurable maximum of in-flight messages. | M |
| `FR-409` | SHALL record per-message processing duration and outcome as metrics, labelled by outcome. | M |

> **▶ Note on FR-406 at the baseline.** Amazon SQS has no operation that moves a single message to its dead-letter queue on demand; redrive happens only after `maxReceiveCount` receives. Immediate dead-lettering of a permanent failure therefore requires V0 to send the message to the DLQ and then delete it from the source queue — two calls, not atomic. This is a degradation **on the baseline itself** and SHALL be recorded in the coupling register, not hidden in a helper.

### 4.5.1 Validation Pipeline

Executed in order. First failing check records its code; remaining checks still run where possible so the citizen receives a complete failure list.

| Step | Check | Failure code |
|---|---|---|
| 1 | Size ≤ configured maximum | `FILE_TOO_LARGE` |
| 2 | MIME sniff on the first 512 bytes; must be `application/pdf` and match `declaredMime` | `UNSUPPORTED_MEDIA_TYPE`, `MIME_MISMATCH` |
| 3 | SHA-256 of the full object | — (recorded, not a pass/fail check) |
| 4 | PDF parses; not encrypted; page count within `[1, maxPages]` | `CORRUPT_DOCUMENT`, `ENCRYPTED_DOCUMENT`, `PAGE_COUNT_OUT_OF_RANGE` |
| 5 | Decode page 1 to a bitmap, downsample to a fixed **64×64 grayscale** matrix, SHA-256 the matrix → `contentDigest` | `RENDER_FAILED` |
| 6 | Render a fixed-size thumbnail PNG and upload via presigned upload | `RENDER_FAILED` |

| ID | Requirement | Pri |
|---|---|---|
| `FR-410` | Steps 1–5 SHALL be **deterministic**: identical input bytes SHALL produce an identical `contentDigest` on every target and every CPU architecture. | M |
| `FR-411` | The thumbnail of step 6 SHALL NOT be asserted byte-identical. | M |
| `FR-412` | The PDF and rasterisation libraries SHALL be version-pinned, and determinism SHALL be verified in CI on both `amd64` and `arm64`. | M |

> **▶ Measurement rationale for §4.5.1.** The pipeline is deliberately CPU-bound and deterministic so that throughput differences between targets are attributable to infrastructure rather than to workload variance. This is why OCR was rejected: it is neither deterministic nor version-stable.
>
> **▶ Why the digest is computed on a downsampled matrix, not on the PNG.** Font rasterisation is not reliably byte-identical across CPU architectures and library builds. Asserting on rendered PNG bytes would generate failures unrelated to the research question. The 64×64 grayscale downsample is a pure numeric reduction and is stable.
>
> `[OPEN — verify early]` If step 5 still proves non-deterministic across architectures in the M0 CI check, fall back to a transform with no rasterisation at all (e.g. a fixed-cost hash chain over the decompressed content stream). Do not spend more than a day on this; the CPU-bound property matters, the specific transform does not.

---

# 5. Interface Contracts

## 5.1 API Conventions

| Aspect | Rule |
|---|---|
| Base path | `/api/v1` (public), `/internal/v1` (service-to-service) |
| Media type | `application/json; charset=utf-8`; errors `application/problem+json` |
| Auth (public) | `Authorization: Bearer <JWT>` |
| Auth (internal) | Network policy + `X-Subject-Id` / `X-Subject-Roles` headers set by `identity-gateway`. Internal endpoints SHALL be unreachable from outside the cluster. |
| Correlation | `X-Correlation-Id` on every request and response (FR-014) |
| Idempotency | `Idempotency-Key` required on `POST` that creates or records a result; the same key replays the original response |
| Concurrency | `ETag` on case reads; `If-Match` required on state-changing calls |
| Pagination | `?limit=` (default 20, max 100), `?cursor=` opaque; response `{ "items": [...], "nextCursor": "..." \| null }` |
| Timestamps | RFC 3339, UTC, millisecond precision |
| Identifiers | UUIDv7, lowercase hyphenated |
| Versioning | Path-versioned. A breaking change requires `/v2` and an update to this document. |

## 5.2 Error Catalogue

All errors use RFC 9457 Problem Details:

```json
{
  "type": "https://ccsra.example/errors/INVALID_STATE_TRANSITION",
  "title": "Invalid state transition",
  "status": 409,
  "detail": "Case is in state ACCEPTED; submit is not permitted.",
  "code": "INVALID_STATE_TRANSITION",
  "correlationId": "01J8...",
  "errors": [ { "field": "state", "message": "..." } ]
}
```

| Code | HTTP | Raised when |
|---|---|---|
| `VALIDATION_FAILED` | 400 | Request body fails schema validation |
| `INVALID_CASE_TYPE` | 400 | `caseType` not in the enum |
| `UNAUTHENTICATED` | 401 | Token missing, malformed, expired, or signature invalid |
| `FORBIDDEN_ROLE` | 403 | Caller's roles do not permit the endpoint |
| `FORBIDDEN_NOT_OWNER` | 403 | Citizen accessing another subject's case (INV-9) |
| `CASE_NOT_FOUND` | 404 | Unknown `caseId`, or hidden by INV-9 |
| `DOCUMENT_NOT_FOUND` | 404 | Unknown `documentId` |
| `INVALID_STATE_TRANSITION` | 409 | Transition not in the §3.2 table |
| `CONCURRENT_MODIFICATION` | 409 | `If-Match` version stale (INV-6) |
| `UPLOAD_NOT_COMPLETED` | 409 | Completion called but no object present |
| `DOCUMENT_ALREADY_VALIDATED` | 409 | Second, non-identical validation result (INV-8) |
| `NO_DOCUMENTS_ATTACHED` | 409 | Submit with zero documents in `UPLOADED` |
| `DOCUMENTS_IMMUTABLE` | 409 | Attach or delete attempted outside `DRAFT` / `NEEDS_INFO` (INV-2) |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | `declaredMime` not accepted |
| `PAYLOAD_TOO_LARGE` | 413 | Body or object exceeds the configured maximum |
| `DOCUMENT_LIMIT_EXCEEDED` | 422 | More than `maxDocumentsPerCase` |
| `DECISION_REASON_REQUIRED` | 422 | `REJECTED` without a reason (T7) |
| `RATE_LIMITED` | 429 | Per-subject rate limit exceeded |
| `UPSTREAM_UNAVAILABLE` | 503 | Downstream service or provider dependency unavailable |
| `INTERNAL_ERROR` | 500 | Unclassified. Logged with full context; `detail` is generic. |

> `CASE_NOT_FOUND` rather than `FORBIDDEN_NOT_OWNER` SHALL be returned when a citizen requests a case belonging to another subject **and** the endpoint is a plain read, so that case existence is not disclosed. `FORBIDDEN_NOT_OWNER` is used on mutations, where the caller already holds the id legitimately.

## 5.3 Public API — `identity-gateway`

### Citizen endpoints

| # | Method | Path | Request | Success | Errors |
|---|---|---|---|---|---|
| 1 | `POST` | `/api/v1/cases` | `{caseType, title}` | `201` `{id, reference, caseType, state:"DRAFT", title, version, createdAt}` + `ETag` | 400, 401, 403, 429 |
| 2 | `GET` | `/api/v1/cases` | `?state=&limit=&cursor=` | `200` `{items:[CaseSummary], nextCursor}` | 401, 403 |
| 3 | `GET` | `/api/v1/cases/{caseId}` | — | `200` `CaseDetail` + `ETag` | 401, 403, 404 |
| 4 | `POST` | `/api/v1/cases/{caseId}/documents` | `{filename, declaredMime, sizeBytes}` | `201` `{documentId, uploadUrl, uploadMethod:"PUT"\|"POST", requiredHeaders:{...}, formFields:{...}, expiresAt}` | 400, 401, 403, 404, 409, 413, 415, 422 |
| 5 | `POST` | `/api/v1/cases/{caseId}/documents/{documentId}/complete` | `{}` + `Idempotency-Key` | `200` `{documentId, status:"UPLOADED", sizeBytes}` | 401, 403, 404, 409, 413 |
| 6 | `GET` | `/api/v1/cases/{caseId}/documents/{documentId}/download` | — | `200` `{downloadUrl, expiresAt}` | 401, 403, 404, 409 |
| 7 | `DELETE` | `/api/v1/cases/{caseId}/documents/{documentId}` | `If-Match` | `204` | 401, 403, 404, 409 |
| 8 | `POST` | `/api/v1/cases/{caseId}/submit` | `If-Match`, `Idempotency-Key` | `202` `{id, state:"VALIDATING", version}` | 401, 403, 404, 409 |
| 9 | `GET` | `/api/v1/cases/{caseId}/events` | `?limit=&cursor=` | `200` `{items:[AuditEntry], nextCursor}` | 401, 403, 404 |

### Caseworker endpoints

| # | Method | Path | Request | Success | Errors |
|---|---|---|---|---|---|
| 10 | `GET` | `/api/v1/worker/cases` | `?state=&caseType=&limit=&cursor=` | `200` `{items:[CaseSummary], nextCursor}` | 401, 403 |
| 11 | `GET` | `/api/v1/worker/cases/{caseId}` | — | `200` `CaseDetail` incl. `validationResults[]` + `ETag` | 401, 403, 404 |
| 12 | `POST` | `/api/v1/worker/cases/{caseId}/decision` | `{decision:"ACCEPTED"\|"REJECTED", reason}` + `If-Match`, `Idempotency-Key` | `200` `{id, state, decidedAt, version}` | 400, 401, 403, 404, 409, 422 |

### Operational endpoints (all four services)

| Method | Path | Auth | Response |
|---|---|---|---|
| `GET` | `/healthz` | none | `200 {"status":"ok"}` |
| `GET` | `/readyz` | none | `200` or `503 {"status":"degraded","checks":{...}}` |
| `GET` | `/metrics` | none, cluster-internal | Prometheus text |

### Representations

```
CaseSummary  { id, reference, caseType, state, title, createdAt, updatedAt, documentCount }
CaseDetail   { ...CaseSummary, version, submittedAt, decidedAt, decidedBy, decisionReason,
               documents: [ { id, filename, declaredMime, detectedMime, sizeBytes, sha256,
                              status, supersedesId, createdAt, uploadedAt, validatedAt,
                              validation: { outcome, failureCodes[], checks } | null } ] }
AuditEntry   { id, occurredAt, actorType, actorId, eventType, correlationId, payload }
```

## 5.4 Internal API — `case-service` (`/internal/v1`)

| Method | Path | Purpose | Notes |
|---|---|---|---|
| `POST` | `/cases` | Create case | Mirrors public #1 |
| `GET` | `/cases` | List | Filtered by `X-Subject-Id` unless caller has `caseworker` |
| `GET` | `/cases/{id}` | Read | |
| `POST` | `/cases/{id}/submit` | T2 / T3 | |
| `POST` | `/cases/{id}/decision` | T6 / T7 | |
| `GET` | `/cases/{id}/events` | Audit trail | |
| `GET` | `/cases/{id}/export` | JSON Lines export (FR-214) | Caseworker only |
| `POST` | `/cases/{id}/documents` | Register a document in `PENDING_UPLOAD`; returns `documentId` + `storageKey` | Called by `intake-service` (FR-303) |
| `PATCH` | `/documents/{id}` | Set `UPLOADED`, `sizeBytes` | Called by `intake-service` (FR-304) |
| `DELETE` | `/documents/{id}` | Mark deleted | Called by `intake-service` (FR-308) |
| `GET` | `/documents/{id}/authz` | Answer "may `X-Subject-Id` access this document?" | Called by `intake-service` (FR-306) |
| `POST` | `/documents/{id}/validation-result` | Record result; may trigger T4 / T5 | Called by `validation-worker`. **Requires `Idempotency-Key`** (FR-206, FR-403) |

## 5.5 Internal API — `intake-service` (`/internal/v1`)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/cases/{caseId}/documents` | Register + issue presigned upload (public #4) |
| `POST` | `/documents/{id}/complete` | Verify object, mark uploaded, publish event (public #5) |
| `GET` | `/documents/{id}/download-url` | Presigned GET after authz check (public #6) |
| `DELETE` | `/documents/{id}` | Delete object + mark deleted (public #7) |
| `POST` | `/objects/presign` | `{key, operation:"GET"\|"UPLOAD", ttlSeconds, contentType?, maxSizeBytes?}` → `{url, method, expiresAt, requiredHeaders, formFields}`. **Worker-only** (FR-307) |

## 5.6 Event Contracts

CloudEvents 1.0, JSON format, published to the broker. `partitionkey` = `caseId` (FR-405).

**Envelope**

```json
{
  "specversion": "1.0",
  "type": "de.masterprojekt.ccsra.document.uploaded.v1",
  "source": "/ccsra/intake-service",
  "id": "01J8XK...",
  "time": "2026-09-25T10:14:22.481Z",
  "subject": "01J8XA...",
  "datacontenttype": "application/json",
  "partitionkey": "01J8XA...",
  "correlationid": "01J8XC...",
  "data": { }
}
```

| Type | Publisher | Consumer | `data` |
|---|---|---|---|
| `document.uploaded.v1` | `intake-service` | `validation-worker` | `{caseId, documentId, storageKey, filename, declaredMime, sizeBytes, uploadedAt}` |
| `document.validated.v1` | `validation-worker` | *(none yet — observability and future notification)* | `{caseId, documentId, outcome, failureCodes[], checks:{detectedMime,sizeBytes,sha256,pageCount,contentDigest,thumbnailKey}, workerVersion, durationMs}` |
| `case.submitted.v1` | `case-service` | *(none yet)* | `{caseId, reference, caseType, subjectId, documentIds[], submittedAt, resubmission:boolean}` |
| `case.decided.v1` | `case-service` | *(none yet)* | `{caseId, reference, decision, reason, decidedBy, decidedAt}` |

| ID | Requirement | Pri |
|---|---|---|
| `FR-020` | Event payloads SHALL be validated against a committed JSON Schema in CI. | M |
| `FR-021` | Event types SHALL be versioned in the type string. A breaking payload change requires a new version suffix. | M |
| `FR-022` | Consumers SHALL tolerate unknown fields (forward compatibility). | M |
| `FR-023` | Delivery is **at-least-once**. Every consumer SHALL be idempotent (FR-403). | M |

> **▶ Design decision: no transactional outbox.** `case-service` publishes after commit; `intake-service` publishes after the object is confirmed. A crash between commit and publish loses an event. This is accepted and documented rather than solved.
>
> Rationale: a transactional outbox is a provider-neutral reliability layer that would partly *mask* the broker coupling this artifact exists to measure — it would make the publish side look uniformly portable regardless of the broker. Record this as an ADR at M0 alongside its consequence: a lost `document.uploaded` leaves a case in `VALIDATING` indefinitely. A reconciliation job that re-publishes for documents stuck in `UPLOADED` beyond a threshold is `[OPEN]` for M2 if it proves necessary during load runs.

## 5.7 Persistence Schema

PostgreSQL. Forward-only migrations (FR-213). Sketch, not final DDL:

```sql
CREATE TYPE case_state        AS ENUM ('DRAFT','VALIDATING','NEEDS_INFO','UNDER_REVIEW','ACCEPTED','REJECTED');
CREATE TYPE case_type         AS ENUM ('BUILDING_PERMIT','RESIDENCE_REGISTRATION','BENEFIT_CLAIM');
CREATE TYPE document_status   AS ENUM ('PENDING_UPLOAD','UPLOADED','VALIDATING','VALID','INVALID','SUPERSEDED','DELETED');
CREATE TYPE validation_outcome AS ENUM ('PASS','FAIL');
CREATE TYPE actor_type        AS ENUM ('CITIZEN','CASEWORKER','SYSTEM');

CREATE TABLE cases (
  id              uuid        PRIMARY KEY,
  reference       text        NOT NULL UNIQUE,
  case_type       case_type   NOT NULL,
  state           case_state  NOT NULL,
  subject_id      text        NOT NULL,
  title           text        NOT NULL CHECK (length(title) <= 200),
  version         integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  submitted_at    timestamptz,
  decided_at      timestamptz,
  decided_by      text,
  decision_reason text,
  CONSTRAINT rejected_needs_reason
    CHECK (state <> 'REJECTED' OR decision_reason IS NOT NULL)
);
CREATE INDEX cases_subject_created_idx ON cases (subject_id, created_at DESC, id DESC);
CREATE INDEX cases_state_created_idx   ON cases (state, created_at DESC, id DESC);

CREATE TABLE documents (
  id             uuid            PRIMARY KEY,
  case_id        uuid            NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
  filename       text            NOT NULL CHECK (length(filename) <= 255),
  declared_mime  text            NOT NULL,
  detected_mime  text,
  size_bytes     bigint          CHECK (size_bytes IS NULL OR size_bytes > 0),
  sha256         char(64),
  storage_key    text            NOT NULL UNIQUE,
  status         document_status NOT NULL,
  supersedes_id  uuid            REFERENCES documents(id),
  created_at     timestamptz     NOT NULL DEFAULT now(),
  uploaded_at    timestamptz,
  validated_at   timestamptz
);
-- INV-7
CREATE UNIQUE INDEX documents_active_filename_idx
  ON documents (case_id, filename)
  WHERE status NOT IN ('SUPERSEDED','DELETED');
CREATE INDEX documents_case_idx ON documents (case_id);

CREATE TABLE validation_results (
  id             uuid               PRIMARY KEY,
  document_id    uuid               NOT NULL UNIQUE REFERENCES documents(id) ON DELETE CASCADE,
  outcome        validation_outcome NOT NULL,
  failure_codes  text[]             NOT NULL DEFAULT '{}',
  checks         jsonb              NOT NULL,
  worker_version text               NOT NULL,
  duration_ms    integer            NOT NULL,
  created_at     timestamptz        NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
  id             bigserial   PRIMARY KEY,
  case_id        uuid        NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
  occurred_at    timestamptz NOT NULL DEFAULT now(),
  actor_type     actor_type  NOT NULL,
  actor_id       text        NOT NULL,
  event_type     text        NOT NULL,
  correlation_id text        NOT NULL,
  payload        jsonb       NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_case_idx ON audit_events (case_id, id);
-- INV-3: append-only, enforced by revoking UPDATE/DELETE from the application role

CREATE TABLE processed_messages (
  message_id   text        NOT NULL,
  consumer     text        NOT NULL,
  processed_at timestamptz NOT NULL DEFAULT now(),
  result_ref   text,
  PRIMARY KEY (message_id, consumer)
);
CREATE INDEX processed_messages_age_idx ON processed_messages (processed_at);
```

| ID | Requirement | Pri |
|---|---|---|
| `FR-030` | The application database role SHALL have `UPDATE` and `DELETE` revoked on `audit_events`. | M |
| `FR-031` | State transitions SHALL run at isolation level `READ COMMITTED` with `SELECT ... FOR UPDATE` on the case row, or `REPEATABLE READ` with retry `[OPEN: choose at M2 after measuring contention]`. | M |
| `FR-032` | `processed_messages` rows SHALL be pruned after a configurable retention (default 7 days). | S |
| `FR-033` | The schema SHALL use no PostgreSQL extension and no AWS-specific SQL feature (e.g. Aurora-only functions). Provider coupling for CP-3 lives in the **control plane** (CR-005), not in the schema. | M |

> **▶ Measurement rationale for FR-033.** Keeping the schema vanilla is deliberate. It isolates the CP-3 finding to the operational surface — authentication mode, backup and restore, HA, connectivity — which is the interesting part. If the schema also broke, the two effects would be inseparable.

## 5.8 Object Storage Layout

```
<bucket>/
  cases/{caseId}/documents/{documentId}/original
  cases/{caseId}/documents/{documentId}/thumbnail.png
```

| Aspect | Rule |
|---|---|
| Key immutability | `storageKey` is assigned at registration and never changes (INV-4) |
| Object metadata | `case-id`, `document-id`, `sha256` (set after validation), `uploaded-by` |
| Bucket layout | One bucket per environment; no per-case bucket |
| Public access | Disabled. All access via presigned URLs only. |
| Encryption | Provider-managed server-side encryption at rest |
| Retention | Applied per FR-309 where supported |

---

# 6. Provider Coupling Specification (Variant V0)

> The *justification* for choosing these five coupling points — the sampling argument that answers "representative of what?" — is in [`Research-Design.md` §2](./Research-Design.md). This section specifies only **what to build**.

## 6.1 The Coupling Register

| ID | Requirement | Pri |
|---|---|---|
| `CR-001` | The repository SHALL contain a machine-readable `coupling-register.yaml` describing every coupling point and every capability within it. | M |
| `CR-002` | The register SHALL be the single source of truth for the Capability Matrix produced during evaluation. It SHALL NOT be duplicated in prose. | M |

**Schema**

```yaml
couplingPoints:
  - id: CP-2
    class: object-storage
    service: intake-service
    facet: primary
    capabilities:
      - id: CP-2.presign-upload
        description: Issue a time-limited upload constrained by content-type and maximum size
        requirement: FR-301, FR-302
        baseline:
          provider: aws
          service: Amazon S3
          mechanism: Presigned POST with policy conditions (content-length-range, Content-Type, expiry)
        criticality: high        # feeds the FE weighting
        adapterMethod: DocumentStore.PresignUpload
        conformanceTest: [PresignUpload_EnforcesContentType, PresignUpload_EnforcesMaxSize, PresignUpload_Expires]
        justification: Bytes never pass through the application; the size ceiling is enforced by storage
```

| Field | Meaning |
|---|---|
| `criticality` | `high` \| `medium` \| `low`. Input to the FE weighting scheme. |
| `adapterMethod` | The V1 port method that abstracts this capability. Empty means no abstraction is possible. |
| `conformanceTest` | The tests in §7.4 that verify it. Empty means unverifiable, which is itself a finding. |
| `justification` | The engineering reason required by CR-011. |

The formal schema is `tools/registercheck/coupling-register.schema.json`. `make check-register` validates the register against it and checks that every referenced requirement and test exists in this document.

## 6.2 V0 Native Bindings

V0 uses these directly, with no port, no interface and no indirection.

| ID | CP | Requirement | Pri |
|---|---|---|---|
| `CR-003` | **CP-1** Compute | V0 SHALL use EKS-specific features: **EKS Pod Identity** for pod-level AWS credentials, the AWS Load Balancer Controller with an Application Load Balancer as ingress, the Amazon EBS CSI driver, and EKS managed node groups with explicit instance types. | M |
| `CR-004` | **CP-2** Object storage | V0 SHALL use the AWS SDK for Go v2 S3 client directly: **presigned POST with a policy document** (`content-length-range`, `Content-Type`) for uploads, **S3 Object Lock** in compliance mode for retention, lifecycle rules transitioning objects to an infrequent-access storage class, and SSE-KMS encryption with a customer-managed key. It SHALL NOT use any provider-neutral storage library. | M |
| `CR-005` | **CP-3** Relational data | V0 SHALL use Amazon RDS for PostgreSQL and SHALL depend on control-plane features: **IAM database authentication** (short-lived token, not password), automated backups with point-in-time restore, Multi-AZ deployment, VPC-private connectivity with no public endpoint, and DB parameter groups. | M |
| `CR-006` | **CP-4** Messaging | V0 SHALL use the AWS SDK for Go v2 SQS client against a **FIFO queue** and SHALL depend on SQS-specific semantics: **message groups** (`MessageGroupId` = `caseId`, for FR-405 ordering), message deduplication IDs, a redrive policy to a FIFO dead-letter queue with `maxReceiveCount` (FR-406), and visibility-timeout extension via `ChangeMessageVisibility` (FR-407). | M |
| `CR-007` | **CP-5** Identity | V0 SHALL use an **Amazon Cognito user pool** as user store and token issuer, and SHALL depend on user-pool groups mapped to roles via the `cognito:groups` claim, a client-credentials app client with a resource server and custom scopes for the harness (FR-106), Cognito managed login, and pool-managed user lifecycle. | M |
| `CR-008` | **CP-6** Secrets | V0 SHOULD use AWS Secrets Manager, accessed via EKS Pod Identity, for all secrets. | S |
| `CR-009` | **CP-7** Observability | V0 SHOULD export telemetry to Amazon CloudWatch and AWS X-Ray via the AWS Distro for OpenTelemetry (ADOT) collector. | S |
| `CR-010` | **CP-8** Control plane | The IaC corpus SHALL record, per target, which resources have no equivalent and must be substituted or self-managed. | M |

> **Note on two options deliberately not used.** *Aurora PostgreSQL* (CR-005) would show stronger CP-3 coupling, but it is a PostgreSQL-*compatible* engine rather than PostgreSQL. It would break assumption A-2 and mix an engine change into the CP-3 finding. *Cognito pre-token-generation triggers* (CR-007) are the usual way to put custom claims into access tokens, but they run as AWS Lambda functions and would bring FaaS — a coupling class excluded in [`Research-Design.md` §9](./Research-Design.md) — in through the back door. Roles are therefore carried by the native `cognito:groups` claim. Both exclusions SHALL be recorded as ADRs.

## 6.3 Realism Constraint

| ID | Requirement | Pri |
|---|---|---|
| `CR-011` | Every coupling injection SHALL be **realistic, not gratuitous**: it SHALL correspond to a feature a competent team would plausibly adopt for a stated reason — operability, security, cost or compliance — and that reason SHALL be recorded in `coupling-register.yaml` under `justification`. | M |
| `CR-012` | V0 SHALL NOT contain any dependency introduced solely to make migration harder. Code review against this requirement SHALL be recorded. | M |

> **▶ Measurement rationale.** These two requirements protect construct validity, and they are the ones most likely to be attacked in the defence. A straw-man application engineered to be maximally unportable would inflate every effort metric and invalidate the study. Example justifications to record: RDS IAM database authentication (eliminates stored database passwords — a genuine security improvement); SQS FIFO message groups (the simplest way to get per-case ordering without building it); S3 Object Lock (a real public-records retention requirement).

---

# 7. Variant V1 — Ports and Adapters

## 7.1 Requirements

| ID | Requirement | Pri |
|---|---|---|
| `AR-001` | V1 SHALL implement hexagonal architecture: every primary coupling point SHALL be accessed exclusively through a **port**, with per-target **adapters**. | M |
| `AR-002` | Ports SHALL be expressed in domain vocabulary and SHALL NOT leak provider types, provider error types, or provider-specific options into their signatures. | M |
| `AR-003` | Adapter selection SHALL be driven by configuration at deployment time. Switching adapters SHALL require **no code change and no recompilation**. | M |
| `AR-004` | The domain package SHALL import no adapter package and no provider SDK. This SHALL be enforced by an automated import-boundary check in CI. | M |
| `AR-005` | Every port SHALL expose a sentinel `ErrUnsupported`. An adapter whose provider cannot offer a capability SHALL return it. It SHALL NOT silently no-op and SHALL NOT panic. | M |
| `AR-006` | A **port conformance suite** (§7.4) SHALL define the required semantics of each port and SHALL pass against every adapter. | M |
| `AR-007` | V1 SHALL be deployable to any target with only a Helm values difference, except for resources that genuinely have no equivalent. | M |
| `AR-008` | Where V1 must self-host a capability the target does not provide, that substitution SHALL be recorded in the **operational-debt register** (§7.5) with its own operability cost. It SHALL NOT be counted as portability. | M |

> **▶ Measurement rationale for AR-005.** `ErrUnsupported` is the code-level hook that feeds the Capability Matrix. Without it, an adapter that quietly ignores a retention call would make the target look *more* portable than it is. This one sentinel error is the mechanism by which the application reports its own portability gaps upward into the evaluation model.
>
> **▶ Measurement rationale for AR-008.** Self-hosting is not portability; it is a transfer of operational burden from the provider to the operator. If this is not measured separately, V1 will appear artificially superior to V0 and the RQ3 answer will be wrong.

## 7.2 Port Interfaces

```go
package ports

// ErrUnsupported is returned when the underlying provider cannot offer the
// requested capability at all. Adapters MUST return it rather than silently
// succeeding. It is the input signal for the Capability Matrix. (AR-005)
var ErrUnsupported = errors.New("capability not supported by this provider")

// ---------- CP-2: object storage ----------

type PutConstraints struct {
    ContentType  string
    MaxSizeBytes int64
    TTL          time.Duration
}

type PresignedURL struct {
    URL             string
    Method          string            // "GET", "PUT" or "POST" (form upload)
    RequiredHeaders map[string]string // headers the client MUST send
    FormFields      map[string]string // POST only: policy fields to send before the file
    ExpiresAt       time.Time
}

type ObjectInfo struct {
    Key       string
    SizeBytes int64
    ETag      string
    UpdatedAt time.Time
}

type DocumentStore interface {
    // PresignUpload returns a PUT or POST upload, whichever lets the storage
    // service itself enforce c. (FR-301, FR-302)
    PresignUpload(ctx context.Context, key string, c PutConstraints) (PresignedURL, error)
    PresignGet(ctx context.Context, key string, ttl time.Duration) (PresignedURL, error)
    Head(ctx context.Context, key string) (ObjectInfo, error)
    Delete(ctx context.Context, key string) error
    ApplyRetention(ctx context.Context, key string, until time.Time) error // may return ErrUnsupported
}

// ---------- CP-3: relational state ----------

type CaseRepository interface {
    Create(ctx context.Context, c *domain.Case) error
    Get(ctx context.Context, id uuid.UUID) (*domain.Case, error)
    List(ctx context.Context, f ListFilter) ([]*domain.CaseSummary, Cursor, error)

    // Transition loads the case under a row lock, applies fn, and writes the
    // case, its audit entry and any document changes in ONE transaction.
    // Returns ErrVersionConflict if expectedVersion is stale. (INV-3, INV-6)
    Transition(ctx context.Context, id uuid.UUID, expectedVersion int,
        fn func(*domain.Case) (domain.AuditEntry, error)) error

    // RecordValidationResult is idempotent on idempotencyKey.
    // replayed=true means the key was already seen and nothing changed. (INV-8)
    RecordValidationResult(ctx context.Context, idempotencyKey string,
        r domain.ValidationResult) (replayed bool, err error)
}

// ---------- CP-4: messaging ----------

type Event struct {
    Type          string
    Source        string
    ID            string
    Time          time.Time
    Subject       string
    CorrelationID string
    Data          json.RawMessage
}

type EventBus interface {
    Publish(ctx context.Context, e Event, partitionKey string) error
}

type Outcome int

const (
    OutcomeAck        Outcome = iota // processed successfully
    OutcomeRetry                     // transient failure; redeliver
    OutcomeDeadLetter                // permanent failure; do not redeliver
)

type Delivery struct {
    MessageID     string
    DeliveryCount int // 1 on first delivery
    PartitionKey  string
    Event         Event
}

type EventConsumer interface {
    // Subscribe blocks until ctx is cancelled. Handler outcomes drive
    // acknowledgement, retry and dead-lettering.
    Subscribe(ctx context.Context, handler func(context.Context, Delivery) Outcome) error
}

// ---------- CP-5: identity ----------

type Subject struct {
    ID        string
    Roles     []string
    AuthLevel string
}

type IdentityVerifier interface {
    Verify(ctx context.Context, rawToken string) (Subject, error)
}

// ---------- CP-6: secrets ----------

type SecretProvider interface {
    Get(ctx context.Context, name string) (string, error)
}
```

## 7.3 Adapter Matrix

| Port | AWS adapter | Portable adapter | Selected by |
|---|---|---|---|
| `DocumentStore` | AWS SDK S3 client: presigned POST policy, Object Lock, SSE-KMS | S3 SigV4 presign against a configurable endpoint (SeaweedFS locally) | `CCSRA_ADAPTER_STORAGE=aws-s3\|s3` |
| `CaseRepository` | `pgx` + RDS IAM token auth | `pgx` + password / mTLS | `CCSRA_ADAPTER_REPOSITORY=pg-iam\|pg` |
| `EventBus` / `EventConsumer` | AWS SDK SQS client, FIFO queue | AMQP 1.0 / RabbitMQ | `CCSRA_ADAPTER_BROKER=sqs\|amqp` |
| `IdentityVerifier` | Cognito (JWKS + `cognito:groups`, `client_id` claim names) | Generic OIDC (Keycloak) | `CCSRA_ADAPTER_IDENTITY=cognito\|oidc` |
| `SecretProvider` | Secrets Manager + EKS Pod Identity | Kubernetes secret / env | `CCSRA_ADAPTER_SECRETS=secretsmanager\|k8s` |

> **▶ Note on the two S3 adapters.** With an AWS baseline, the portable `s3` adapter already runs against Amazon S3. The separate `aws-s3` adapter exists only for the features the portable one does not assume — POST policies, Object Lock, SSE-KMS. Expect the V0/V1 gap at CP-2 to be small on the baseline. That is a result to report, not a defect to engineer away.

| ID | Requirement | Pri |
|---|---|---|
| `AR-009` | An unknown adapter name SHALL cause startup to fail immediately with a clear error. The service SHALL NOT start with a silently defaulted adapter. | M |
| `AR-010` | Each service SHALL log its resolved adapter set at startup, and expose it as a labelled gauge on `/metrics`. | M |

> `AR-010` matters more than it looks: during measurement campaigns it is the only cheap way to be certain which configuration a given run actually exercised.

## 7.4 Port Conformance Suite

> This suite is the **executable operationalization of functional equivalence** (Data Act Art. 2(37): "materially comparable outcome in response to the same input"). It is the strongest link between the artifact and the regulation, and it is what the FE score in [`Research-Design.md` §5.2](./Research-Design.md) is scored against.

| ID | Requirement | Pri |
|---|---|---|
| `AR-011` | The suite SHALL run against a live instance of each adapter's backing service — never against a mock. | M |
| `AR-012` | Each test SHALL record pass, fail, or **unsupported** (`ErrUnsupported`) as a distinct outcome, and emit machine-readable results. | M |
| `AR-013` | Suite results SHALL be published per target as evidence for the Capability Matrix. | M |

### `DocumentStore`

| Test | Required semantics |
|---|---|
| `PresignUpload_EnforcesContentType` | An upload with a different `Content-Type` than presigned is rejected by the storage service |
| `PresignUpload_EnforcesMaxSize` | An upload exceeding `MaxSizeBytes` is rejected by the storage service, whichever method (`PUT`/`POST`) the adapter chose |
| `PresignUpload_Expires` | After `TTL` elapses the upload returns 4xx |
| `PresignGet_ReturnsExactBytes` | Downloaded bytes are byte-identical to what was uploaded |
| `Head_ReportsExactSize` | `Head` after upload returns the exact byte count |
| `Delete_IsIdempotent` | Deleting a non-existent key returns nil, not an error |
| `ApplyRetention_BlocksDeleteOrReportsUnsupported` | Either delete-before-expiry fails, **or** `ErrUnsupported` is returned. Silent success is a test failure. |

### `EventBus` / `EventConsumer`

| Test | Required semantics |
|---|---|
| `Publish_AtLeastOnceDelivery` | Every published message is delivered at least once |
| `SamePartitionKey_PreservesOrder` | Messages with one partition key arrive in publish order |
| `DifferentPartitionKeys_MayInterleave` | No ordering guarantee is asserted across keys |
| `Retry_IncrementsDeliveryCount` | `OutcomeRetry` causes redelivery with `DeliveryCount` incremented |
| `DeadLetter_RemovesFromMainFlow` | `OutcomeDeadLetter` stops redelivery and the message remains retrievable elsewhere |
| `MaxDeliveryCount_DeadLetters` | Repeated retries eventually dead-letter automatically |
| `LeaseRenewal_SurvivesSlowHandler` | A handler running longer than the lease duration (lock or visibility timeout) does not cause duplicate delivery |
| `DuplicateDelivery_IsToleratedByHandler` | Same message twice produces one recorded effect |

### `CaseRepository`

| Test | Required semantics |
|---|---|
| `Transition_IsAtomicWithAudit` | On handler error nothing is written — neither state nor audit entry |
| `Transition_RejectsStaleVersion` | Concurrent transitions: exactly one succeeds, the other gets `ErrVersionConflict` |
| `RecordValidationResult_IsIdempotent` | Same idempotency key twice → `replayed=true`, one stored row |
| `AuditLog_IsAppendOnly` | Update or delete on an audit row fails |

### `IdentityVerifier`

| Test | Required semantics |
|---|---|
| `Verify_RejectsExpired` | `exp` in the past → error |
| `Verify_RejectsWrongAudience` | Wrong `aud` → error |
| `Verify_RejectsWrongIssuer` | Wrong `iss` → error |
| `Verify_RejectsBadSignature` | Tampered signature → error |
| `Verify_MapsClaimsDeterministically` | The same token always yields the same `Subject` |
| `Verify_HandlesKeyRotation` | A token signed with a newly rotated key validates after JWKS refresh |

## 7.5 Operational-Debt Register

| ID | Requirement | Pri |
|---|---|---|
| `AR-014` | Every self-hosted substitution SHALL be recorded in `operational-debt.yaml`. | M |

```yaml
substitutions:
  - couplingPoint: CP-5
    target: eu-a
    replaced: Amazon Cognito user pool
    substitute: Keycloak, self-hosted on Kubernetes
    reason: no managed identity service offered by this provider
    assumedResponsibilities:
      - patching and version upgrades
      - backup and restore of the user store
      - high availability and failover
      - certificate and key rotation
      - capacity planning
    estimatedOpsHoursPerMonth: 4      # documented estimate, not a measurement
    residualDegradation: "No managed MFA; no conditional access policies."
```

---

# 8. Non-Functional Requirements

| ID | Requirement | Pri |
|---|---|---|
| `NFR-001` | **Reproducibility.** A third party SHALL be able to reproduce a full deployment and measurement run from the public repository and its documentation alone. | M |
| `NFR-002` | **Determinism.** Given identical input bytes, the validation pipeline SHALL produce an identical `contentDigest` on every target and architecture (FR-410). | M |
| `NFR-003` | **Traceable design.** Every design decision affecting a measurable quantity SHALL be recorded as an ADR in `docs/adr/`. | M |
| `NFR-004` | **Cost containment.** Every environment SHALL be destroyable to zero standing cost; a budget alert SHALL be configured per target. | M |
| `NFR-005` | **Openness.** Application, IaC, harness, schemas and datasets SHALL be published under an OSI-approved licence. | M |
| `NFR-006` | **Data protection.** No real personal data SHALL be used at any point. All identities and documents SHALL be synthetic and generated by the harness. | M |
| `NFR-007` | **Security hygiene.** No credentials in version control; secrets injected at deploy time; object storage never publicly readable; public endpoints authenticated and rate-limited. | M |
| `NFR-008` | **Comparability over scale.** Load profiles SHALL be sized for comparability, not for stress. Absolute scale is explicitly not a goal. | M |
| `NFR-009` | **Size budget.** Each variant SHOULD remain under ~5000 LOC excluding generated code and tests. Exceeding it requires an ADR. | S |
| `NFR-010` | **Startup.** Each service SHALL reach readiness within 10 s of container start under normal conditions. | S |
| `NFR-011` | **Image size.** Each container image SHOULD be under 50 MB. | S |
| `NFR-012` | **No hidden state.** Services SHALL be stateless apart from their designated store. Restarting any pod SHALL NOT lose committed work. | M |

> **▶ Note on NFR-009.** The size budget is a scope-control instrument, not a style preference. Artifact scope creep is the highest-probability risk in this project ([`Research-Design.md` §10, RSK-6](./Research-Design.md)); a stated budget makes the creep visible while it is still cheap to reverse.

---

# 9. Deployment and Configuration

## 9.1 Container Requirements

| ID | Requirement | Pri |
|---|---|---|
| `DR-001` | Each service SHALL build to a single static binary in a `distroless` or `scratch` image, running as a non-root user. | M |
| `DR-002` | Images SHALL be built for `linux/amd64` and `linux/arm64` and SHALL be referenced by digest, never by mutable tag. | M |
| `DR-003` | Each image SHALL embed its build SHA, exposed via `/healthz` and used as `workerVersion`. | M |

## 9.2 Helm Values Contract

One chart per service, one umbrella chart per variant. Target differences SHALL be expressible entirely in values (AR-007).

```yaml
image:      { repository, digest }
replicas:   2
resources:  { requests: {cpu, memory}, limits: {cpu, memory} }
adapters:   { storage, repository, broker, identity, secrets }   # V1 only
config:     { }        # non-secret env vars
secrets:    { }        # secret refs, never literal values
ingress:    { enabled, className, host }
telemetry:  { otlpEndpoint }
labels:     { experimentId, variant, target, runId }
```

| ID | Requirement | Pri |
|---|---|---|
| `DR-004` | Charts SHALL be byte-identical across targets. Only values files differ. | M |
| `DR-005` | Every deployed object SHALL carry the `experimentId`, `variant`, `target` and `runId` labels for cost attribution. | M |
| `DR-006` | Secrets SHALL be referenced, never inlined in a values file. | M |

## 9.3 Configuration Contract

**All services**

| Variable | Default | Meaning |
|---|---|---|
| `CCSRA_SERVICE_NAME` | — | Logical service name |
| `CCSRA_VARIANT` | — | `v0` \| `v1` |
| `CCSRA_TARGET` | — | `aws` \| `eu-a` \| `eu-b` \| `local` |
| `CCSRA_HTTP_ADDR` | `:8080` | Listen address |
| `CCSRA_LOG_LEVEL` | `info` | |
| `CCSRA_SHUTDOWN_DRAIN_SECONDS` | `30` | FR-016 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | Empty disables export |

**`identity-gateway`**

| Variable | Default | Meaning |
|---|---|---|
| `CCSRA_OIDC_ISSUER` | — | Expected `iss` |
| `CCSRA_OIDC_AUDIENCE` | — | Expected `aud` |
| `CCSRA_OIDC_JWKS_URL` | — | Discovered if empty |
| `CCSRA_OIDC_JWKS_TTL_SECONDS` | `3600` | FR-102 |
| `CCSRA_CLAIM_SUBJECT` | `sub` | Claim carrying the subject id |
| `CCSRA_CLAIM_ROLES` | `roles` | Claim carrying roles (`cognito:groups` on the AWS baseline) |
| `CCSRA_CASE_SERVICE_URL` | — | |
| `CCSRA_INTAKE_SERVICE_URL` | — | |
| `CCSRA_UPSTREAM_TIMEOUT_MS` | `5000` | |
| `CCSRA_RATE_LIMIT_RPS` | `50` | Per subject, FR-107 |
| `CCSRA_MAX_BODY_BYTES` | `65536` | FR-108 |
| `CCSRA_ADAPTER_IDENTITY` | — | V1 only |

**`case-service`**

| Variable | Default | Meaning |
|---|---|---|
| `CCSRA_DB_HOST` / `_PORT` / `_NAME` / `_USER` | — | |
| `CCSRA_DB_AUTH_MODE` | `password` | `password` \| `iam` (CR-005) |
| `CCSRA_DB_PASSWORD` | — | Secret ref; unused when `iam` |
| `CCSRA_DB_SSLMODE` | `require` | |
| `CCSRA_DB_MAX_CONNS` | `20` | |
| `CCSRA_MAX_DOCUMENTS_PER_CASE` | `10` | |
| `CCSRA_PROCESSED_MSG_RETENTION_DAYS` | `7` | FR-032 |
| `CCSRA_BROKER_*` | — | Publish connection |
| `CCSRA_ADAPTER_REPOSITORY`, `CCSRA_ADAPTER_BROKER` | — | V1 only |

**`intake-service`**

| Variable | Default | Meaning |
|---|---|---|
| `CCSRA_STORAGE_BUCKET` | — | Bucket name |
| `CCSRA_STORAGE_ENDPOINT` | — | Empty = provider default |
| `CCSRA_PRESIGN_TTL_SECONDS` | `900` | FR-302 |
| `CCSRA_MAX_OBJECT_BYTES` | `20971520` | 20 MiB |
| `CCSRA_ALLOWED_MIME` | `application/pdf` | |
| `CCSRA_RETENTION_DAYS` | `30` | FR-309 |
| `CCSRA_CASE_SERVICE_URL` | — | |
| `CCSRA_ADAPTER_STORAGE`, `CCSRA_ADAPTER_BROKER` | — | V1 only |

**`validation-worker`**

| Variable | Default | Meaning |
|---|---|---|
| `CCSRA_BROKER_*` | — | Consume connection |
| `CCSRA_SUBSCRIPTION` | `validation` | Queue / subscription name |
| `CCSRA_MAX_INFLIGHT` | `8` | FR-408 |
| `CCSRA_MAX_DELIVERY_COUNT` | `5` | FR-406 |
| `CCSRA_LEASE_RENEW_ENABLED` | `true` | FR-407 |
| `CCSRA_MAX_PAGES` | `50` | |
| `CCSRA_MAX_OBJECT_BYTES` | `20971520` | |
| `CCSRA_INTAKE_SERVICE_URL` | — | FR-401, FR-307 |
| `CCSRA_ADAPTER_BROKER` | — | V1 only |

| ID | Requirement | Pri |
|---|---|---|
| `DR-007` | Configuration SHALL be validated at startup. A missing or malformed required variable SHALL abort startup with a message naming the variable. | M |
| `DR-008` | No default SHALL point at a live cloud resource. Defaults resolve to the local stack or to nothing. | M |

## 9.4 Resource Normalisation

| ID | Requirement | Pri |
|---|---|---|
| `DR-009` | Every service SHALL declare identical CPU/memory requests and limits across all targets. | M |
| `DR-010` | Replica counts SHALL be fixed and identical across targets. Horizontal autoscaling SHALL be disabled during measurement. | M |
| `DR-011` | Node pools SHALL be sized to the nearest equivalent instance type across targets; the mapping and any unavoidable deviation SHALL be documented per target. | M |

| Service | Replicas | CPU req/limit | Mem req/limit |
|---|---|---|---|
| `identity-gateway` | 2 | 250m / 500m | 128Mi / 256Mi |
| `case-service` | 2 | 500m / 1000m | 256Mi / 512Mi |
| `intake-service` | 2 | 250m / 500m | 128Mi / 256Mi |
| `validation-worker` | 2 | 1000m / 2000m | 512Mi / 1Gi |

> `[OPEN]` Provisional. Fix at M2 after a baseline profiling run, then **freeze** — these values are an internal-validity control, and changing them mid-study invalidates cross-target comparison.

## 9.5 Local Development Stack

| ID | Requirement | Pri |
|---|---|---|
| `DR-012` | The repository SHALL provide a `docker compose` stack running PostgreSQL, an S3-compatible store (SeaweedFS, ADR 0009), RabbitMQ and Keycloak, seeded and ready. | M |
| `DR-013` | `make dev` SHALL start the stack and all four services; `make test` SHALL run unit, port-conformance and E2E suites against it. | M |
| `DR-014` | The full V1 application, the port conformance suite and the E2E suite SHALL be runnable locally with **no cloud account and no cloud spend**. | M |
| `DR-015` | A `kind` configuration SHALL allow the Helm charts to be exercised locally before any cloud deployment. | S |

> **▶ Why DR-014 is a MUST.** The local stack is exactly the V1 portable adapter set — PostgreSQL, S3-compatible storage, AMQP, OIDC. That means the entire abstracted variant can be built and validated before a single euro of cloud budget is committed, and budget approval ([`Research-Design.md` §11, D-1](./Research-Design.md)) is still the blocking open item. This requirement de-risks the schedule more than any other in the document.

---

# 10. Test and Acceptance

## 10.1 Test Levels

| Level | Scope | Runs against | Gate |
|---|---|---|---|
| **Unit** | Domain logic, state machine, validation pipeline | In-process, no I/O | Every commit |
| **Port conformance** (§7.4) | Each adapter against its real backing service | Local stack; then each target | Every commit (local); every deployment (target) |
| **Integration** | One service + its real dependencies | Local stack | Every commit |
| **E2E conformance** (§10.2) | Full system through the public API | Local stack; then every deployment | Every deployment, both variants |
| **Load** | k6 scenarios | Deployed targets only | Measurement campaigns |

| ID | Requirement | Pri |
|---|---|---|
| `FR-040` | The state machine SHALL have exhaustive unit tests: every transition in §3.2, and a rejection test for every state/trigger pair not in the table. | M |
| `FR-041` | Every invariant in §3.3 SHALL have at least one test that attempts to violate it and asserts failure. | M |
| `FR-042` | Import boundaries SHALL be checked in CI (AR-004). | M |
| `FR-043` | Determinism (FR-410) SHALL be verified in CI on both architectures against the full corpus. | M |

## 10.2 E2E Conformance Suite

| ID | Requirement | Pri |
|---|---|---|
| `FR-044` | A single E2E suite SHALL exist, parameterised only by base URL and credentials. | M |
| `FR-045` | It SHALL pass **unchanged** against V0 and V1 on every target (FR-006). | M |
| `FR-046` | It SHALL exercise the public API only — never internal endpoints, the database, or storage directly. | M |
| `FR-047` | It SHALL be self-cleaning: every run creates its own cases and leaves no state that affects a later run. | M |

> **▶ Measurement rationale.** This suite is the operational definition of "the application still works after the switch". If it passes on a target, functional equivalence has been achieved *at the application level* — which is a different and weaker claim than per-capability equivalence, and the thesis must keep the two apart. The port conformance suite (§7.4) makes the stronger, per-capability claim.

## 10.3 Test Corpus

| ID | Requirement | Pri |
|---|---|---|
| `FR-048` | A deterministic synthetic PDF corpus SHALL be committed, or generated by a pinned generator from a committed seed. | M |
| `FR-049` | No corpus file SHALL contain real personal data (NFR-006). | M |

| Fixture | Property | Expected outcome |
|---|---|---|
| `valid-1page.pdf` | 1 page, ~50 KB | `PASS` |
| `valid-10page.pdf` | 10 pages, ~2 MB | `PASS` |
| `valid-large.pdf` | just under the size limit | `PASS` |
| `oversized.pdf` | just over the size limit | `FAIL FILE_TOO_LARGE` |
| `mime-mismatch.pdf` | a PNG named `.pdf` | `FAIL MIME_MISMATCH` |
| `corrupt.pdf` | truncated | `FAIL CORRUPT_DOCUMENT` |
| `encrypted.pdf` | password-protected | `FAIL ENCRYPTED_DOCUMENT` |
| `too-many-pages.pdf` | above `maxPages` | `FAIL PAGE_COUNT_OUT_OF_RANGE` |

## 10.4 Acceptance Criteria

| ID | Given | When | Then | Verifies |
|---|---|---|---|---|
| `AC-01` | An authenticated citizen | `POST /cases` with a valid `caseType` | `201`, state `DRAFT`, unique `reference`, `version` 1, `ETag` present | FR-201 |
| `AC-02` | An unauthenticated caller | Any `/api/v1` request | `401 UNAUTHENTICATED` | FR-101 |
| `AC-03` | A token with role `citizen` | `GET /api/v1/worker/cases` | `403 FORBIDDEN_ROLE` | FR-104 |
| `AC-04` | Citizen A owns case X | Citizen B calls `GET /cases/X` | `404 CASE_NOT_FOUND` (existence not disclosed) | FR-209, INV-9 |
| `AC-05` | A case in `DRAFT` | `POST /cases/{id}/documents` | `201` with a presigned upload (method, URL, required headers, form fields), expiry; document in `PENDING_UPLOAD` | FR-301, FR-303 |
| `AC-06` | A valid presigned upload | Upload with a different `Content-Type`, or larger than the declared maximum | Storage rejects with 4xx | FR-302 |
| `AC-07` | A presigned upload past its TTL | Upload attempt | 4xx | FR-302 |
| `AC-08` | Bytes uploaded to the presigned URL | `POST .../complete` | `200`, document `UPLOADED`, `document.uploaded` published | FR-304 |
| `AC-09` | No object was uploaded | `POST .../complete` | `409 UPLOAD_NOT_COMPLETED` | FR-305 |
| `AC-10` | A case in `DRAFT` with zero uploaded documents | `POST /submit` | `409 NO_DOCUMENTS_ATTACHED` | FR-202, T2 |
| `AC-11` | A case in `DRAFT` with one uploaded document | `POST /submit` | `202`, state `VALIDATING`, `case.submitted` published | FR-202, FR-208 |
| `AC-12` | A case in `VALIDATING`, all documents valid | Last `document.validated` with `PASS` recorded | State becomes `UNDER_REVIEW` within 5 s | FR-207, T4 |
| `AC-13` | A case in `VALIDATING` with one invalid document | Last result recorded | State becomes `NEEDS_INFO`; failure codes visible on the document | FR-207, T5 |
| `AC-14` | A case in `NEEDS_INFO` | Citizen uploads a replacement with the same filename | Prior document becomes `SUPERSEDED`; new one is active | FR-211, INV-7 |
| `AC-15` | A case in `NEEDS_INFO` with a new uploaded document | `POST /submit` | `202`, state `VALIDATING`, `case.submitted` with `resubmission:true` | T3 |
| `AC-16` | A case in `UNDER_REVIEW` | Caseworker posts `ACCEPTED` | `200`, state `ACCEPTED`, `decidedAt` and `decidedBy` set, `case.decided` published | T6 |
| `AC-17` | A case in `UNDER_REVIEW` | Caseworker posts `REJECTED` with no reason | `422 DECISION_REASON_REQUIRED` | T7 |
| `AC-18` | A case in `ACCEPTED` | Any transition attempt | `409 INVALID_STATE_TRANSITION` | INV-1 |
| `AC-19` | A case at version 3 | `POST /submit` with `If-Match: "2"` | `409 CONCURRENT_MODIFICATION` | FR-203, INV-6 |
| `AC-20` | Two concurrent `submit` calls on one case | Both issued simultaneously | Exactly one succeeds; the other returns 409; exactly one `case.submitted` published | FR-203, INV-6 |
| `AC-21` | A case in `UNDER_REVIEW` | Citizen attempts to attach a document | `409 DOCUMENTS_IMMUTABLE` | INV-2 |
| `AC-22` | A `document.uploaded` message | Delivered twice | Exactly one `ValidationResult` stored; second call replays the first response | FR-403, INV-8 |
| `AC-23` | `corrupt.pdf` uploaded | Validation runs | Document `INVALID` with `CORRUPT_DOCUMENT`; case reaches `NEEDS_INFO` | FR-402, §4.5.1 |
| `AC-24` | A message whose processing always fails permanently | Delivered | Dead-lettered without exhausting retries; case does not hang in `VALIDATING` beyond the alert threshold | FR-406 |
| `AC-25` | Ten documents in one case uploaded concurrently | Validation runs | All ten validated; events for this `caseId` processed in publish order | FR-405 |
| `AC-26` | The same corpus file | Validated on two different targets and two architectures | `contentDigest` is identical | FR-410, NFR-002 |
| `AC-27` | Any state change | Occurs | Exactly one `AuditEvent` exists, in the same transaction, with the request's `correlationId` | FR-204, INV-3 |
| `AC-28` | An `AuditEvent` row | Update or delete attempted by the application role | Fails | FR-030, INV-3 |
| `AC-29` | A V1 service | Started with an unknown adapter name | Startup fails with a message naming the variable | AR-009 |
| `AC-30` | A V1 service | Started | Logs and exposes its resolved adapter set | AR-010 |
| `AC-31` | A target without object retention support | `ApplyRetention` called | `ErrUnsupported` returned; a `capability_unsupported` record logged; request still succeeds | AR-005, FR-309 |
| `AC-32` | Any request | Sent without `X-Correlation-Id` | One is generated, echoed, and present in downstream logs, events and audit entries | FR-014 |
| `AC-33` | A service | Sent `SIGTERM` during in-flight work | In-flight work completes; exit code 0; no message lost | FR-016 |
| `AC-34` | The full E2E suite | Run against V0 and V1 on the same target | Identical results, unchanged suite | FR-006, FR-045 |
| `AC-35` | A citizen's own case | `GET /cases/{id}/events` | Full audit trail, paginated | FR-212 |
| `AC-36` | A caseworker | `GET /worker/cases?state=UNDER_REVIEW` | Only cases in that state, cursor-paginated, across all subjects | FR-210 |

---

# 11. Build Scope and Phasing

Application-level only. Thesis-level phasing is in [`Research-Design.md` §8](./Research-Design.md).

| Stage | Deliverable | Depends on cloud budget? |
|---|---|---|
| **B0** | Repo skeleton, ADR process, `coupling-register.yaml` schema, CI, local stack (`DR-012`–`DR-014`) | **No** |
| **B1** | Domain model, state machine, invariants, exhaustive unit tests | **No** |
| **B2** | V1 ports + portable adapters (S3, plain PostgreSQL, AMQP, generic OIDC); port conformance suite green locally | **No** |
| **B3** | All four services wired; E2E suite green on the local stack | **No** |
| **B4** | Helm charts; `kind` deployment green | **No** |
| **B5** | V0: AWS-native direct-SDK implementation; OpenTofu for AWS; deploy D0-AWS | Yes |
| **B6** | V1 AWS adapters; deploy D1-AWS | Yes |
| **B7** | k6 harness; baseline measurement campaigns | Yes |
| **B8** | EU target adapters and IaC; deploy D0-A, D1-A | Yes |

> **B0–B4 require no cloud account.** Build V1 first, against the local stack, then derive V0 from it. This inverts the naive order and is deliberate: it removes budget approval from the critical path for the first four stages.
>
> **▶ Caveat to state in the thesis.** Building V1 before V0 risks making V0 look like a "de-abstracted" V1 rather than a natively written application. Mitigation: V0 lives in its own tree with no shared code (Appendix A), is written against the AWS SDK for Go v2 documentation rather than by deleting interfaces from V1, and is reviewed against `CR-011`/`CR-012` for realism. Record this ordering decision and its mitigation as an ADR — an examiner will ask.

---

# 12. Traceability

| Requirements | Verified by | CP | Service |
|---|---|---|---|
| FR-001, FR-002 | Architecture review; CI import check | — | all |
| FR-003, FR-004, FR-005 | CI dependency check (AR-004); code review | all | all |
| FR-006 | AC-34 | — | all |
| FR-010 – FR-013 | Smoke tests per deployment | — | all |
| FR-014 | AC-32 | — | all |
| FR-015, FR-017 | DR-007 startup tests; error-contract tests | — | all |
| FR-016 | AC-33 | — | all |
| FR-020 – FR-023 | Schema validation in CI; AC-22 | CP-4 | all publishers |
| FR-030 | AC-28 | CP-3 | case-service |
| FR-031 | AC-20 | CP-3 | case-service |
| FR-032, FR-033 | Migration review; schema lint | CP-3 | case-service |
| FR-040 – FR-043 | CI gates | — | all |
| FR-044 – FR-049 | Suite execution per deployment | — | all |
| FR-100 – FR-102 | AC-02; `Verify_*` conformance tests | CP-5 | identity-gateway |
| FR-103 | `Verify_MapsClaimsDeterministically` | CP-5 | identity-gateway |
| FR-104 | AC-03 | CP-5 | identity-gateway |
| FR-105 – FR-108 | Integration tests; AC-02 | CP-5 | identity-gateway |
| FR-200 – FR-202 | AC-01, AC-10, AC-18 | CP-3 | case-service |
| FR-203 | AC-19, AC-20 | CP-3 | case-service |
| FR-204 | AC-27, AC-28 | CP-3 | case-service |
| FR-205 – FR-207 | AC-08, AC-12, AC-13, AC-22 | CP-3 | case-service |
| FR-208 | AC-11, AC-16 | CP-4 (publish) | case-service |
| FR-209 | AC-04 | CP-3 | case-service |
| FR-210 – FR-212 | AC-14, AC-35, AC-36 | CP-3 | case-service |
| FR-213, FR-214 | Migration test; export test | CP-3 | case-service |
| FR-300 – FR-303 | AC-05, AC-06, AC-07 | CP-2 | intake-service |
| FR-304, FR-305 | AC-08, AC-09 | CP-2 | intake-service |
| FR-306 – FR-308 | Download and delete integration tests | CP-2 | intake-service |
| FR-309, FR-310 | AC-31 | CP-2 | intake-service |
| FR-400 – FR-404 | AC-08, AC-12, AC-22, AC-23 | CP-4 | validation-worker |
| FR-405 | AC-25 | CP-4 | validation-worker |
| FR-406 – FR-409 | AC-24; `DeadLetter_*`, `LeaseRenewal_*` | CP-4 | validation-worker |
| FR-410 – FR-412 | AC-26; CI matrix build | — | validation-worker |
| CR-001 – CR-010 | Register completeness check; deployment review | all | all |
| CR-011, CR-012 | Recorded justification review | all | V0 |
| AR-001 – AR-004 | CI import-boundary check | all | V1 |
| AR-005 | AC-31 | all | V1 |
| AR-006 | Port conformance suite (§7.4) | all | V1 |
| AR-007 | Chart diff check across targets | CP-1 | V1 |
| AR-008, AR-014 | Operational-debt register review | all | V1 |
| AR-009, AR-010 | AC-29, AC-30 | all | V1 |
| AR-011 – AR-013 | Suite execution and result publication per target | all | V1 |
| NFR-001 – NFR-012 | Reproduction run; CI checks; review | — | all |
| DR-001 – DR-015 | Deployment pipeline checks; `make dev` / `make test` | CP-1 | all |
| DR-016, DR-017 | Repository-layout review; effort-diff attribution check | all | both variants |

---

# Appendix A — Repository Layout

```
.
├── SRS.md                          this document
├── Research-Design.md              research questions, method, evaluation model
├── README.md
├── coupling-register.yaml          §6.1 — feeds the Capability Matrix
├── operational-debt.yaml           §7.5
├── docs/adr/                       architecture decision records
├── go.work                         ties the Go modules together (ADR 0008)
├── Makefile                        dev, test, ci, stack-up/-down/-reset, docs
│
├── app/                            one Go module per variant (ADR 0008)
│   ├── v0/                         PROVIDER-NATIVE VARIANT
│   │   ├── cmd/{identity-gateway,case-service,intake-service,validation-worker}/
│   │   └── internal/               direct AWS SDK for Go v2 use, no ports
│   │
│   └── v1/                         ABSTRACTED VARIANT
│       ├── cmd/{identity-gateway,case-service,intake-service,validation-worker}/
│       └── internal/
│           ├── domain/             entities, state machine, invariants — imports no SDK
│           ├── ports/              §7.2 interfaces
│           ├── adapters/
│           │   ├── aws/            aws-s3, sqs, cognito, pg-iam, secretsmanager
│           │   └── portable/       s3, amqp, oidc, pg, k8ssecret
│           └── platform/           config, http, errors, observability
│
├── api/
│   ├── openapi.yaml                §5.3 public API
│   └── events/*.schema.json        §5.6 event payload schemas
│
├── db/migrations/                  §5.7 forward-only
│
├── test/
│   ├── conformance/                §7.4 port conformance — runs against real backends
│   ├── e2e/                        §10.2 — one suite, both variants, every target
│   └── corpus/                     §10.3 deterministic synthetic PDFs
│
├── deploy/
│   ├── helm/{charts,values/<target>-<variant>.yaml}
│   ├── tofu/{modules,targets/<target>}
│   └── local/{docker-compose.yaml,kind.yaml,keycloak/,seaweedfs/}
│
├── harness/k6/                     load scenarios S1–S5
│
├── scripts/check-boundaries.sh     AR-004, FR-003–FR-005 import check
├── tools/                          registercheck (Go module, register schema), md2docx.js
└── .github/workflows/ci.yaml       runs `make ci`
```

| ID | Requirement | Pri |
|---|---|---|
| `DR-016` | `app/v0` and `app/v1` SHALL share **no** Go package. Duplication between them is intended. | M |
| `DR-017` | `test/e2e`, `test/corpus`, `api/` and `db/migrations/` SHALL be shared by both variants. | M |

> **▶ Measurement rationale for DR-016.** This is the requirement most likely to feel wrong to write and it is the one that must not be relaxed. Migration effort is computed from `git diff` **within** each tree. Shared code would appear in both diffs and contaminate both effort figures. It would also make V0 not genuinely native — it would be V1 with the interfaces removed, which is a different thing and a weaker comparison.
>
> Cost: V0 is roughly a 60%-sized re-implementation (no ports, no adapter layer, fewer tests), so the total build is about 1.6× a single application rather than 2×. Budget for that at B5 rather than discovering it there.
