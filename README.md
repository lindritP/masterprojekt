# Masterprojekt — Cloud Portability under EU Data Act Art. 30

Master's thesis project: building a measurable cloud-native reference application in order to
operationalize the Data Act's notion of **functional equivalence** and measure what it actually
costs to switch a workload from a hyperscaler to an EU-based provider.

## Documents

| Document | Answers | Read it if you want to know |
|---|---|---|
| [`SRS.md`](./SRS.md) | **What to build** | The services, their contracts, the data model, the coupling injections, the tests |
| [`Research-Design.md`](./Research-Design.md) | **Why, and how it is measured** | Research questions, method, provider selection procedure, evaluation model, validity, risks |
| [`docs/adr/`](./docs/adr/) | **Why it is built this way** | Architecture decision records (NFR-003) |
| [`coupling-register.yaml`](./coupling-register.yaml) | **What depends on the provider** | Every coupling point and capability, with its justification (SRS §6.1) |
| [`instructions.txt`](./instructions.txt) | — | Original project notes (source material, superseded) |

The two documents are deliberately separable: `SRS.md` can be handed to an implementer without
`Research-Design.md`, and `Research-Design.md` can be read by a supervisor without `SRS.md`.

## The idea in one paragraph

EU Data Act Art. 30(1) obliges cloud providers to enable **functional equivalence** after a
customer switches provider. Art. 2(37) defines the term. Nothing defines how to *measure* it.
This project builds a small public-sector case-submission application that deliberately embodies
five distinct classes of provider dependency — compute, object storage, relational data,
asynchronous messaging and identity — in two variants: one written natively against AWS, one
written behind provider-agnostic ports. Both are deployed to AWS and to EU providers, and every
incompatibility encountered during the move is logged, classified and measured. The output is a
reusable Portability Assessment Model.

## Getting started

Requirements: Go 1.26, Docker with Compose v2, `make`. No cloud account is needed until stage B5.

```bash
make help              # list all targets
make stack-up          # PostgreSQL (migrated), SeaweedFS (S3), RabbitMQ (topology), Keycloak
make ci                # gofmt, go vet, corpus + unit tests, import boundaries, coupling register
make test-conformance  # port conformance suite against the local stack (SRS §7.4)
make stack-down        # stop the stack, keep data (stack-reset also deletes it)
```

| Local endpoint | Address |
|---|---|
| PostgreSQL | `localhost:5432`, database `ccsra`; the application connects as `ccsra_app` |
| S3 (SeaweedFS) | `http://localhost:8333`, bucket `ccsra-documents` (Object Lock enabled) |
| RabbitMQ | `amqp://localhost:5672`, vhost `ccsra`; management UI on `http://localhost:15672` |
| Keycloak | `http://localhost:8180`, realm `ccsra` (issuer `http://localhost:8180/realms/ccsra`) |

All local credentials are test values defined in
[`deploy/local/docker-compose.yaml`](./deploy/local/docker-compose.yaml),
[`deploy/local/seaweedfs/s3.json`](./deploy/local/seaweedfs/s3.json) and the realm file
[`deploy/local/keycloak/ccsra-realm.json`](./deploy/local/keycloak/ccsra-realm.json)
(roles `citizen`/`caseworker`, two client-credentials harness clients, three synthetic users).
They are never used on a deployed target.

`make docs` regenerates `SRS.docx` and `Research-Design.docx` from the Markdown sources (needs Node.js).

## Status

Stages **B0–B2 complete** (SRS §11), all without a cloud account. Hyperscaler baseline: AWS
`eu-central-1` (ADR 0003).

- **B0** — repository layout, one Go module per variant, ADR process, coupling register with
  schema and checker, import-boundary check, CI, local stack.
- **Determinism spike** — the validation pipeline (PDFium as WebAssembly) yields identical
  content digests on amd64 and arm64 (FR-410, ADR 0010); CI re-checks it on both.
- **B1** — domain model, case state machine and invariants (`app/v1/internal/domain`), with a
  test for every state/trigger pair (FR-040) and a violation attempt per invariant (FR-041).
- **B2** — V1 ports, the portable adapters (S3, PostgreSQL, AMQP 1.0, OIDC, env secrets) and the
  port conformance suite: 27/27 green against the local stack.

The four services are still stubs. Next: stage **B3** — wire the services, so the application
runs end to end on the local stack and the E2E suite (SRS §10.2) passes.
