# Research Design

**Measuring Functional Equivalence: A Portability Assessment Model for Cloud-Native Workloads under Article 30 of the EU Data Act**

| | |
|---|---|
| **Document** | Research Design & Method |
| **Version** | 0.3 |
| **Date** | 2026-11-01 |
| **Author** | Lindrit Prekaj |
| **Context** | Master's Thesis — preparatory project phase |
| **Companion** | [`SRS.md`](./SRS.md) — specification of the software artifact |
| **Status** | Open decisions marked `[OPEN]`; assumptions marked `[ASSUMPTION]` |

> **Scope of this document.** This is the *why*. It defines the research gap, the questions, the method, the experimental design, how the EU provider is selected, and the evaluation model that turns the experiment into results. It contains no build instructions.
>
> The *what* — the reference application, its contracts, data model and coupling injections — is specified in [`SRS.md`](./SRS.md). The two documents are deliberately separable: the SRS can be handed to an implementer without this document.

---

## Revision History

| Version | Date | Change |
|---|---|---|
| 0.1 | 2026-09-20 | Initial combined SRS draft. |
| 0.2 | 2026-09-25 | Split into `SRS.md` (artifact) and this document (research). Implementation language fixed to Go (closes D-2). |
| 0.3 | 2026-11-01 | Hyperscaler baseline switched from Azure to **AWS** (closes D-10, D-8). Sampling table (§2.2) re-derived: CP-2 becomes the de-facto-standard case and Art. 30(2)–(3) test case #1; CP-4 becomes the proprietary-interface case. New §2.6 justifies the baseline choice. Central claim (§5.1), experimental-design labels (§3.2), risks (§10) and egress handling (§5.5) updated. |

---

# 1. Problem, Gap and Questions

## 1.1 Problem Statement

Regulation (EU) 2023/2854 (**Data Act**), applicable since 12 September 2025, obliges providers of data processing services to remove obstacles to switching (Chapter VI, Art. 23–31). Article 30(1) requires providers of *infrastructure* services to enable **functional equivalence** after a switch. Article 2(37) defines functional equivalence as the re-establishment of a *minimum level of functionality* such that the destination service delivers a *materially comparable outcome* for the same input, for shared features under contract.

The regulation therefore creates a **legal obligation stated in technical terms but accompanied by no technical measurement methodology.** Neither "minimum level of functionality" nor "materially comparable outcome" is operationalized in the regulation, in the harmonised-standards work foreseen under Art. 35, or — to the author's knowledge — in the research literature.

> **Research gap.** There is no validated, reusable method for *measuring* whether functional equivalence has been achieved when a cloud-native workload is switched between providers, and consequently no way to verify compliance with Art. 30 or to compare providers on this dimension.

## 1.2 Research Questions

**RQ (main)**
> How can the functional equivalence and the switching effort of a cloud-native workload between a hyperscaler and EU-based providers be systematically measured, and how effectively does a provider-agnostic abstraction layer reduce provider coupling?

| ID | Question |
|---|---|
| **RQ1** — *Measurement construct* | Which observable, reproducible indicators operationalize the Data Act's notion of *functional equivalence* (Art. 2(37), Art. 30(1)) for a cloud-native workload? |
| **RQ2** — *Coupling and effort* | Which classes of coupling between an application and provider-managed services cause the greatest switching effort, and how is that effort distributed across application code, infrastructure code, data, and operational responsibility? |
| **RQ3** — *Effectiveness of abstraction* | To what extent does a provider-agnostic abstraction layer (hexagonal ports/adapters + Kubernetes + OpenTofu) reduce switching effort, and what performance, cost and complexity penalty does it impose? |
| **RQ4** — *Parity of EU alternatives* | Where do EU-based providers fail to reach functional equivalence with the hyperscaler baseline, and which capability classes are affected? |

> **Note on RQ3.** RQ3 is an addition relative to the initial project outline. The outline proposed an abstraction layer but no un-abstracted comparison condition. Without a control condition, the effectiveness claim in the main RQ is not testable. See §3.

## 1.3 Regulatory Scope

Scope is deliberately narrow: **Data Act Art. 30 only.**

| In scope | Rationale |
|---|---|
| Art. 2(37) — definition of *functional equivalence* | The construct to be operationalized. |
| Art. 30(1) — functional equivalence for IaaS | The obligation whose verifiability is missing. |
| Art. 30(2)–(3) — open interfaces for non-IaaS | Produces the sharpest finding (§5.1). |
| Art. 23 — removal of switching obstacles | Framing for the Break-Point Log. |
| Art. 30(4) / Art. 35 — harmonised standards | Gap evidence: which semantics standards fail to cover. |

| Out of scope | Rationale |
|---|---|
| Art. 25 (contractual terms), Art. 28 (international access), Art. 29 (switching charges) | Not measurable with a technical artifact. Including them would create a method/scope mismatch. |
| GDPR / data-protection analysis | Different discipline. The artifact uses only synthetic data (`SRS.md` §8), which removes the need. |
| Chapter II–V of the Data Act | Unrelated to switching. |

---

# 2. Sampling Rationale: Why These Coupling Points

This section answers the supervisor question **"How is the choice of microservices justified — representative of what?"** and is intended to be reusable as thesis text.

## 2.1 The unit of analysis is the coupling point, not the application

Portability is not a property of an application. It is a property of each point at which the application depends on a provider-supplied capability. An application is portable exactly to the degree that its coupling points are. Therefore:

- The **unit of analysis** is the coupling point (CP).
- The reference application is the **vehicle** that carries the CPs.
- The sampling question is not *"is this application typical?"* but *"do these CPs cover the variation space of coupling types?"*

## 2.2 Sampling strategy: maximum-variation purposive sampling

The five primary coupling points form a **maximum-variation purposive sample** along two orthogonal dimensions:

1. **Interface standardisation** — is the provider's interface a published open standard, a de-facto standard, or proprietary?
2. **State ownership** — does switching require moving persistent state, and of what kind?

On the AWS baseline the five CPs cover **every level** of the standardisation dimension: an open standard (CP-1), a de-facto standard (CP-2), an open standard on paper with proprietary semantics behind it (CP-5, and the data plane of CP-3), and a fully proprietary interface (CP-4). Two of them — CP-2 and CP-5 — are **compliant with a widely implemented interface yet expected to be non-equivalent in function**. That is exactly the situation Art. 30(2)–(3) does not address.

| CP | Coupling class | AWS service | Interface standardisation | State to migrate | Expected coupling | Role in the design |
|---|---|---|---|---|---|---|
| **CP-1** | Compute / orchestration | Amazon EKS | Open standard (Kubernetes API) | None (stateless) | **Low** | **Control case** — establishes that low coupling is achievable and that the instrument can detect it. |
| **CP-2** | Object storage | Amazon S3 | **De-facto standard** (S3 API, re-implemented by many EU providers) | Bulk binary objects | **Low–medium** | **Art. 30(2)–(3) test case #1** — same API, partially equivalent semantics (presigned POST policies, Object Lock, storage classes, KMS encryption). Tests whether "S3-compatible" implies "equivalent". |
| **CP-3** | Relational data | Amazon RDS for PostgreSQL | Open standard at the **data plane**; proprietary **control plane** | Schema + rows + backups | **Medium** | Separates data-plane from operational-plane portability. |
| **CP-4** | Asynchronous messaging | Amazon SQS (FIFO) | **Proprietary** API (HTTP/JSON, no AMQP) | In-flight messages | **High** | **Proprietary-interface case** — no protocol standard to fall back on; message groups, visibility timeout and redrive must be re-mapped onto a different broker model. The reference point for what switching costs when no open interface exists. |
| **CP-5** | Identity / access | Amazon Cognito user pools | Open standard *on paper* (OIDC/OAuth 2.0); proprietary user store, groups, triggers | User identities + credentials (password hashes not exportable) | **Very high** | **Art. 30(2)–(3) test case #2** — standards compliance with near-zero practical portability. |

Secondary, cross-cutting coupling points (lower priority): **CP-6** secrets/key management (Secrets Manager, KMS), **CP-7** observability (CloudWatch, X-Ray), **CP-8** control plane / IaC (AWS resource model, `hashicorp/aws` provider).

## 2.3 The defensible claim

> The five primary coupling points are not a random sample of cloud services. They are a **purposive maximum-variation sample** spanning the range from *fully standardised and stateless* (CP-1) to *nominally standardised but practically non-portable and stateful* (CP-5), with a fully proprietary interface (CP-4) as the reference point at the far end of the standardisation axis. The claim is not that a given application is typical, but that **any** cloud-native application's coupling points fall into these classes, and that the assessment model therefore generalises by **construct coverage** rather than statistical sampling.

## 2.4 Why the public-sector case-workflow domain

| Criterion | Justification |
|---|---|
| **Regulatory alignment** | Public-administration workloads are the primary addressee of EU digital-sovereignty policy. Domain and regulatory motivation reinforce each other rather than being independent. |
| **All coupling classes load-bearing** | Identity is genuinely required (citizen authentication, not decoration). Object storage is required (document upload). Asynchronous messaging is required (validation is long-running). Relational state is required (case records, auditability). No coupling point is artificial. |
| **Retention and audit semantics** | Public records imply retention, immutability and audit-trail requirements — capabilities where provider parity is known to be weak (WORM/immutability). This surfaces an Art. 30 gap that a generic CRUD app would not. |
| **Realistic without being large** | The workflow is understandable in one paragraph, yet is not a toy. Buildable by one person. |

The domain is explicitly **not** claimed to be statistically representative of cloud workloads. Representativeness is claimed at coupling-class level only (§2.3).

## 2.5 Derivation of the microservices

Each service is the minimal realistic carrier of exactly one primary coupling point.

| Service | Carries | Why this service is the minimal carrier |
|---|---|---|
| `identity-gateway` | **CP-5** | Token validation and claim mapping must live at the edge; isolating them confines identity coupling to one deployable unit and makes the migration diff attributable. |
| `case-service` | **CP-3** | The only service owning relational state. Confines data coupling; makes data migration measurable in isolation. |
| `intake-service` | **CP-2** | Issues pre-signed upload URLs. Confines object-storage coupling; pre-signing is the specific mechanism with poor cross-provider equivalence. |
| `validation-worker` | **CP-4** | The only queue consumer. Confines messaging-semantics coupling; makes end-to-end asynchronous latency measurable. |

> **Design constraint (enforced in `SRS.md` FR-003):** each primary coupling point is carried by **exactly one** service. This is a measurement requirement, not an architectural preference — it makes every break point attributable to exactly one coupling class without confounding.

## 2.6 Why AWS as the hyperscaler baseline

| Criterion | Justification |
|---|---|
| **Relevance of the switching origin** | AWS holds the largest IaaS market share in published market analyses `[verify with a dated source at M1]`. A switching study is most useful where switching most often starts. |
| **Full spread on the standardisation axis** | AWS supplies a de-facto-standard case (S3) that an Azure baseline lacks, while still supplying a nominally-standardised identity case (OIDC) and a proprietary case (SQS). See §2.2. |
| **Comparability with prior work** | Much of the cloud-portability and lock-in literature uses AWS as its reference provider, so results can be set against existing findings. |
| **IaC maturity** | The `hashicorp/aws` provider is the most mature OpenTofu/Terraform provider. Baseline IaC effort is therefore not inflated by tooling friction, which keeps `ME_iac` attributable to coupling. |
| **A sovereignty contrast inside one vendor** | The AWS European Sovereign Cloud (generally available since January 2026, first region in Brandenburg) offers AWS APIs in a separate, EU-located partition. It fails PS-01 (§4.3) and is not a target, but it gives the discussion a sharp question: does sovereignty require a change of interface, or only of operator? |

**Trade-offs, stated openly.** (1) The German public sector relies heavily on Microsoft. The domain argument of §2.4 is therefore about the coupling classes a case workflow needs, not about which vendor a German authority typically uses. (2) With SQS instead of Service Bus, CP-4 no longer tests an open-but-non-equivalent protocol (AMQP). That evidence role moves to CP-2. (3) The portable S3 adapter already runs against Amazon S3, so the V0/V1 gap at CP-2 on the baseline is expected to be small. That is a result to report, not a flaw.

> An earlier draft (v0.2) used Azure. The change of baseline is recorded in the revision history and as an ADR. The PAM itself needed no change, which is weak but real evidence for ER-07.

---

# 3. Method and Experimental Design

## 3.1 Method

Design Science Research per the DSRM process model (Peffers et al.), with an embedded **controlled migration experiment**.

| DSRM activity | Instantiation |
|---|---|
| 1. Problem identification | Art. 30 creates a measurement obligation without a measurement method (§1.1). |
| 2. Objectives of a solution | RQ1–RQ4; requirements in `SRS.md`. |
| 3. Design & development | Reference app V0/V1, IaC, harness, PAM v1. |
| 4. Demonstration | Deploy to baseline; migrate to EU-A (and EU-B). |
| 5. Evaluation | Apply PAM; compare V0 vs V1; replication study (§7). |
| 6. Communication | Thesis, open-source repository, published dataset. |

## 3.2 Experimental Design — 2 × N repeated measures

| | AWS (baseline) | EU-A | EU-B *(SHOULD)* |
|---|---|---|---|
| **V0 — provider-native** | D0-AWS *(reference)* | D0-A | — |
| **V1 — abstracted** | D1-AWS | D1-A | D1-B |

- **Factor 1 (treatment):** abstraction layer present (V1) vs absent (V0) → **RQ3**
- **Factor 2:** deployment target → **RQ2, RQ4**

| Comparison | Yields |
|---|---|
| D0-AWS → D0-A | Migration effort **without** abstraction — the baseline switching cost |
| D1-AWS → D1-A | Migration effort **with** abstraction |
| Δ of the two above | **Effectiveness of the abstraction layer (RQ3)** |
| D0-AWS vs D1-AWS | **Cost of portability** — the performance, cost and complexity penalty paid on the baseline for portability not yet used |
| D1-A → D1-B | Marginal cost of the *second* switch — tests whether abstraction amortizes |

> The V0/V1 pair on the baseline (D0-AWS vs D1-AWS) is what turns "abstraction reduces lock-in" from an assertion into a measured trade-off. It is the methodological core of the design.

## 3.3 Measurement Harness Requirements

These requirements belong to the research instrument, not the application.

| ID | Requirement | Pri |
|---|---|---|
| `MR-01` | The harness SHALL execute identical k6 load scenarios against every deployment without modification. | M |
| `MR-02` | Scenarios SHALL include separately: **S1** case creation (CP-3-dominant), **S2** document upload via pre-signed URL (CP-2-dominant), **S3** end-to-end submit→validated (CP-4-dominant, asynchronous), **S4** authentication/token acquisition (CP-5-dominant), **S5** a mixed realistic profile. | M |
| `MR-03` | The harness SHALL record p50/p90/p95/p99 latency, throughput, error rate, and for S3 the **end-to-end asynchronous completion latency** (upload → `document.validated`). | M |
| `MR-04` | Migration-effort metrics SHALL be computed automatically from version control: changed lines and files per category (application code / IaC / Helm / config), and changed OpenTofu resources (added/removed/modified) via plan analysis. | M |
| `MR-05` | The researcher SHALL maintain a structured **Break-Point Log** — one machine-readable record per incompatibility — with: id, coupling point, target, variant, description, severity, resolution strategy, person-hours, residual degradation. | M |
| `MR-06` | The harness SHALL produce a **Capability Matrix** per target: for each capability in the Coupling Register (`SRS.md` §6.1), a rating in {Equivalent, Degraded, Substituted-self-managed, Absent} with evidence. | M |
| `MR-07` | Each measurement campaign SHALL be repeated at least **N = 5** times `[OPEN: confirm N]` in independent runs after a defined warm-up, reporting mean, standard deviation and confidence intervals. | M |
| `MR-08` | Load SHALL be generated from a fixed location not co-located with any target; baseline RTT per target SHALL be characterised and reported. | M |
| `MR-09` | All raw measurement data SHALL be persisted in an open format (CSV/JSON) and published. | M |
| `MR-10` | Cost data SHALL be collected per run via resource tags and reconciled against the analytic cost model (§5.5). | S |

> **`MR-05` deserves emphasis.** The Break-Point Log is the **primary qualitative dataset** of this thesis. It, not the latency numbers, is what answers RQ2. It must be filled in *during* the migration, not reconstructed afterwards — a reconstruction is a recall-biased artifact and a construct-validity failure.

## 3.4 Infrastructure Control Requirements

| ID | Requirement | Pri |
|---|---|---|
| `IR-01` | Every deployment's infrastructure SHALL be described in OpenTofu. Any unavoidable manual step SHALL be logged as a break point with rationale. | M |
| `IR-02` | The IaC corpus SHALL be structured as `modules/` (reusable) + `targets/<target>/` (target-specific) to make the target-specific surface directly countable. | M |
| `IR-03` | A full environment SHALL be creatable from zero and destroyable to zero by a single documented command. | M |
| `IR-04` | Every deployment SHALL be reproducible from a pinned commit: provider versions, chart versions and image digests pinned. | M |
| `IR-05` | All resources SHALL be tagged with `experiment-id`, `variant`, `target`, `run-id` for automated cost attribution. | M |
| `IR-06` | Kubernetes workloads SHALL be packaged as Helm charts, identical across targets except for values. | M |
| `IR-07` | Compute resources SHALL be normalised across targets (equal vCPU, memory, replica count, storage performance tier); unavoidable deviations SHALL be documented as controlled confounders. | M |

> `IR-07` is the single most important internal-validity control in the study. Performance deltas are meaningless if node hardware differs uncontrolled.

---

# 4. Provider Selection Procedure

This answers the supervisor question **"How should the EU provider be chosen?"** The contribution is the *procedure*; the provider is its output.

## 4.1 Principle

> `PS-00` — The selection criteria and weights **MUST be fixed and documented before any provider is evaluated**, and the completed funnel including rejected candidates and rejection reasons **MUST be published**. This is a pre-registration measure against selection and confirmation bias.

## 4.2 Stage 0 — Candidate Pool

Illustrative pool, to be finalised in M1:

STACKIT · IONOS Cloud · OVHcloud · Open Telekom Cloud · Scaleway · Hetzner Cloud · Cleura · UpCloud · plusserver · gridscale

> **No capability claim about any provider may be taken from this document.** Service catalogues change frequently. All catalogue data must be re-verified from primary sources (provider documentation and price lists) during M1 and **dated** in the thesis.

## 4.3 Stage 1 — Hard Inclusion Criteria (K.O.)

| ID | Criterion | Rationale |
|---|---|---|
| `PS-01` | Legal entity established in the EU/EEA, not controlled by a non-EU parent subject to extraterritorial data-access law. | Defines "EU-sovereign" operationally; without it the comparison is not about sovereignty. Excludes EU-located partitions of non-EU hyperscalers, such as the AWS European Sovereign Cloud (see §2.6). |
| `PS-02` | Offers a **managed Kubernetes** service. | CP-1 must exist on the target, otherwise the control case is destroyed. |
| `PS-03` | Offers **at least three** of the four remaining primary capability classes (object storage, managed relational DB, managed message broker, managed identity). | Ensures the migration is non-trivial but not vacuous. |
| `PS-04` | Publicly available list prices. | Quote-only pricing makes the Cost Delta unmeasurable. |
| `PS-05` | Self-service account creation with trial or pay-as-you-go. | Feasibility within thesis timeline and budget. |
| `PS-06` | Maintained Terraform/OpenTofu provider, or an OpenStack-compatible API. | Manual click-ops would make IaC-diff effort measurement impossible. |
| `PS-07` | Public technical documentation in English or German. | Feasibility. |

## 4.4 Stage 2 — Weighted Scoring

Surviving candidates scored 0–3 per criterion. Weights indicative; **must be fixed before scoring**.

| ID | Criterion | Weight `[OPEN]` | Anchor |
|---|---|---|---|
| `PS-08a` | Capability coverage against the 5 primary CPs | 30% | 3 = managed equivalent for all five; 0 = compute only. A service offering an S3- or SQS-compatible API counts as a managed equivalent here; *how* compatible it is gets measured later, not assumed. |
| `PS-08b` | Data Act readiness: switching clauses, exit support, egress-fee policy in ToS/SLA | 20% | Direct relevance to Art. 23/29/30 |
| `PS-08c` | Certification / sovereignty assurance (BSI C5, SecNumCloud, ISO 27001, EUCS readiness) | 15% | Public evidence only |
| `PS-08d` | IaC / API maturity (provider version, resource coverage, release cadence) | 15% | Determines effort-measurement fidelity |
| `PS-08e` | Documentation quality and completeness | 10% | Internal validity: poor docs inflate effort for reasons unrelated to coupling |
| `PS-08f` | Cost at the reference workload profile | 10% | From §5.5 |

## 4.5 Stage 3 — Feasibility Pilot

> `PS-09` — The two top-ranked candidates **MUST** pass a timeboxed pilot before final selection: provision a managed Kubernetes cluster, an object-storage bucket and a managed PostgreSQL instance via IaC, and deploy a trivial container — within **8 person-hours** per provider. Failure disqualifies. The pilot log is a study artifact.

## 4.6 Contrast Requirement

> `PS-10` — Where two EU providers are selected, they **SHOULD** be *architecturally contrasting*: one "hyperscaler-like" provider with a broad managed-service catalogue, and one IaaS-heavy provider with a thin managed layer.

Rationale: the difference between them is itself a finding. It tests whether "EU provider" is a meaningful category for portability purposes, or whether variance *within* EU providers exceeds variance between EU providers and the hyperscaler — a directly publishable result, and a cheap one.

---

# 5. Evaluation Model (PAM) — the Contribution

## 5.1 Mapping to the Data Act

| Provision | What it requires | PAM construct |
|---|---|---|
| Art. 2(37) — *functional equivalence* | Minimum functionality; materially comparable outcome for same input | **FE score** (§5.2) + the **port conformance suite** (`SRS.md` §7.4) as the executable test of "same input → comparable outcome" |
| Art. 30(1) — IaaS functional equivalence | Reasonable measures to enable FE | FE score per coupling point; Capability Matrix |
| Art. 30(2)–(3) — open interfaces for non-IaaS | Open interfaces free of charge | CP-2 and CP-5 as test cases: **interface openness ≠ functional equivalence**; CP-4 as the proprietary reference point |
| Art. 23 — removal of switching obstacles | Technical obstacles removed | **Break-Point Log** — empirical enumeration of surviving obstacles |
| Art. 30(4) / Art. 35 — standards | Compatibility with harmonised standards | Gap evidence: which semantics current standards fail to cover |

> **The sharpest single claim available to this thesis:** neither an open standard (OIDC) nor a de-facto standard (the S3 API) is enough to reach functional equivalence, although both satisfy the letter of Art. 30(2)–(3). CP-5 and CP-2 are designed to produce that evidence. CP-4 shows the contrast: how much more a switch costs when no common interface exists at all. This is a policy-relevant finding, not merely an engineering one.

## 5.2 Functional Equivalence Score (FE)

For each capability `c` in the Coupling Register (`SRS.md` §6.1), on target `t`:

| Rating | Value | Definition |
|---|---|---|
| Equivalent | 1.00 | Managed equivalent exists; conformance tests pass unchanged |
| Degraded | 0.50 | Equivalent exists but with reduced semantics, weaker SLA, or a manual workaround |
| Substituted (self-managed) | 0.25 | No managed equivalent; capability re-established by self-hosting, transferring operational burden |
| Absent | 0.00 | No equivalent; functionality lost or workflow altered |

`FE(cp, t) = Σ w_c · rating(c, t) / Σ w_c`, where `w_c` is the criticality weight of capability `c`.

> `[OPEN]` The criticality weighting scheme must be defined and defended in M1, and subjected to the sensitivity analysis required by `ER-06`.

## 5.3 Migration Effort (a vector, not a scalar)

| Symbol | Metric | Source |
|---|---|---|
| `ME_code` | Changed LOC / files / modules in application code | git diff, categorised |
| `ME_iac` | OpenTofu resources added/removed/modified; changed LOC | plan analysis |
| `ME_cfg` | Changed Helm values / config keys | git diff |
| `ME_data` | Volume migrated; wall-clock migration time; downtime | migration run log |
| `ME_hours` | Logged person-hours per break point | Break-Point Log |
| `ME_debt` | Count and weight of self-managed substitutions | Capability Matrix (rating 0.25) |

Reported both absolute and normalised as a share of the baseline artifact's size.

> **`ME_hours` is the weakest metric in the set.** It is self-reported by a single researcher, and a learning-curve confounder makes the *second* migration systematically cheaper than the first. It must be reported with that caveat, and the objective, tool-derived `ME_code`/`ME_iac` must carry the main argument. Mitigations: alternate migration order where feasible; report per-break-point rather than per-migration totals.

## 5.4 Performance Delta

`PD_latency(t) = p95(t) / p95(baseline)` per scenario; `PD_throughput(t) = max_sustained_rps(t) / max_sustained_rps(baseline)`.

Reported with confidence intervals from `N` repetitions (MR-07), under normalisation constraint IR-07, with per-target baseline RTT reported (MR-08).

## 5.5 Cost Delta

A fixed **reference workload profile** (cases/month, documents/case, mean document size, storage growth, egress volume) is defined once and applied to every target.

`CD(t)` is reported as: monthly list-price TCO; cost per 1000 completed case submissions; and a **separate egress line**, since egress pricing is the direct subject of Art. 29.

The egress line SHALL show both the list price and the amount actually charged for the switching transfer. AWS waives data-transfer-out charges for customers leaving AWS (since March 2024; EU switching handled under its EU Data Act Addendum), and Art. 29 bans switching charges from 12 January 2027. The gap between list price and charged price is itself an observation. Collecting it does not bring Art. 29 into scope (§1.3): it is reported, not legally assessed.

> `[OPEN]` The reference workload profile must be fixed in M1 and **must not be changed afterwards.**

## 5.6 Operational Overhead

Count of components whose lifecycle responsibility (patching, backup, HA, upgrade, monitoring) shifts from provider to operator, weighted by a documented scheme. Sourced from `ME_debt` and the operational-debt register (`SRS.md` §7.5).

## 5.7 Model Requirements

| ID | Requirement | Pri |
|---|---|---|
| `ER-01` | The PAM SHALL define an FE score per coupling point with a documented rating scheme and criticality weighting. | M |
| `ER-02` | The PAM SHALL define a **Break-Point Taxonomy** classifying each break point by coupling class × severity × resolution strategy. | M |
| `ER-03` | Migration Effort SHALL be defined as a vector (§5.3), not a scalar. | M |
| `ER-04` | The PAM SHALL define Performance Delta, Cost Delta and Operational Overhead as in §5.4–5.6. | M |
| `ER-05` | Results SHALL be presented primarily as a **portability profile** (multi-dimensional), not a single number. | M |
| `ER-06` | Any composite index SHALL be accompanied by an explicit weighting rationale and a **sensitivity analysis** showing how ranking changes under alternative weightings. | M |
| `ER-07` | The PAM SHALL be **provider- and application-agnostic**: applying it elsewhere SHALL require new inputs only, not model changes. | M |
| `ER-08` | Each PAM metric SHALL be explicitly mapped to the Data Act provision it operationalizes. | M |
| `ER-09` | The PAM SHOULD be applied by a second person to a data subset to establish inter-rater reliability of the subjective ratings. | S |

> `ER-07` is what makes this a contribution rather than a case report. `ER-09` is cheap and disproportionately strengthens the evaluation chapter — the FE rating scheme is the part of the model most vulnerable to the objection *"these scores are just your opinion."*

---

# 6. Validity Threats and Mitigations

| Type | Threat | Mitigation |
|---|---|---|
| **Construct** | Changed LOC is a poor proxy for effort. | Report an effort *vector* (§5.3), not a scalar; triangulate tool-derived metrics with the Break-Point Log; report break-point counts by severity. |
| **Construct** | FE ratings are subjective. | Published rating anchors; port conformance suite as objective evidence; inter-rater check (ER-09); sensitivity analysis (ER-06). |
| **Construct** | A straw-man unportable V0 would inflate all results. | `SRS.md` CR-011/CR-012: every coupling injection must carry a recorded engineering justification. Consider brief expert review of V0's realism. |
| **Internal** | Hardware/region differences confound performance. | IR-07 normalisation; report deviations; RTT characterisation (MR-08). |
| **Internal** | Learning curve makes later migrations cheaper. | Report per-break-point effort; vary migration order; state the confounder explicitly. |
| **Internal** | Provider-side noisy neighbours. | N repetitions at different times of day (MR-07); report variance. |
| **External** | One application, one domain. | Representativeness claimed at coupling-class level (§2.3), not application level; partial replication (§7). |
| **External** | Provider catalogues change. | All catalogue observations dated; method designed to be re-runnable. |
| **External** | Results are tied to one hyperscaler baseline (AWS); an Azure or GCP origin would shift individual CPs along the standardisation axis. | Claims stated per coupling class and interface type, not per vendor (§2.2); PAM is provider-agnostic (ER-07); the v0.2 Azure mapping is kept as a documented counterfactual. |
| **Conclusion** | Small N, no inferential statistics. | Report descriptive statistics with CIs; make no significance claims. |
| **Reliability** | Single researcher. | Full automation; public repository; ADRs; pre-registered criteria (PS-00). |

---

# 7. Replication Study

| ID | Requirement | Pri |
|---|---|---|
| `RP-01` | The PAM SHOULD be applied to a **second, architecturally different** application to test model generalisability (ER-07). | S |
| `RP-02` | The second application SHOULD be an **existing third-party open-source benchmark**, not a newly built one, to avoid author bias in its construction. | S |
| `RP-03` | The replication SHOULD measure only whether the PAM can be applied and yields interpretable results — not a full V0/V1 comparison. | S |

> `RP-02` is deliberate: applying the model to an application the author did not design is far stronger evidence for ER-07 than applying it to a second self-built app. Candidates to assess in M1: Google Online Boutique, Sock Shop, TrainTicket, DeathStarBench — chosen on how many of the five coupling classes each actually exercises. `[OPEN]`

---

# 8. Phasing

The thesis does not need to be completed in the current semester. Phases P0–P2 are this semester's target.

| Phase | Milestone | Content | Exit criterion |
|---|---|---|---|
| **P0** | M0 — Foundations | Repo structure, ADR process, Coupling Register schema, draft reference workload profile, local dev stack running | SRS approved; local stack green |
| **P1** | M1 — Selection & design | Provider selection (§4) executed and published; criticality weights fixed; workload profile frozen; replication benchmark chosen | EU-A (and EU-B) selected; pre-registration published |
| **P2** | M2 — Baseline | V0 built and deployed to AWS; harness operational; baseline measurements collected | D0-AWS measured with N repetitions |
| **P3** | M3 — Abstraction | V1 built; port conformance suite passing; D1-AWS deployed and measured | Cost-of-portability figure available |
| **P4** | M4 — Migration | D0-A and D1-A executed; Break-Point Log complete; Capability Matrix for EU-A | RQ2 and RQ3 answerable |
| **P5** | M5 — Second EU target *(S)* | D1-B | Marginal-switch cost available |
| **P6** | M6 — Model & replication | PAM finalised; sensitivity analysis; RP-01…03 | RQ1 and RQ4 answerable |
| **P7** | M7 — Write-up | Thesis, dataset publication | Submission |

> **A defensible thesis exists at the end of P4.** P5 and P6 strengthen it. This is the intended fallback line if budget or timeline erode.

---

# 9. Out of Scope

| Excluded | Justification |
|---|---|
| Multi-cloud active/active or federated deployment | Different problem (availability), not switching. |
| Live migration with zero downtime | Art. 30 concerns switching, not continuity. Would dominate effort. |
| GDPR / data-protection legal analysis | Different discipline; synthetic-data-only removes the need. |
| Legal analysis of contractual switching clauses (Art. 25, 28) | Not measurable with a technical artifact. |
| SaaS switching | Art. 30 IaaS/PaaS focus; SaaS not instrumentable. |
| Security or penetration testing of targets | Different research question. |
| Serverless / FaaS as a coupling point | Would add a sixth CP with very poor EU coverage; high dead-end risk. Future work. |
| Absolute performance optimisation | Comparability, not performance, is the goal. |
| On-premises deployment target | Deferred; adds a hardware procurement dependency. |
| Production-grade operations (on-call, DR drills) | Not required to answer any RQ. |

---

# 10. Risks

| ID | Risk | Impact | Prob. | Mitigation |
|---|---|---|---|---|
| `RSK-1` | Multi-provider budget not approved | **Blocking** | Med | Resolve before M1. Environments destroyable to zero; the local dev stack (`SRS.md` §9.5) lets all of V1 be built at zero cloud spend. Fallback: one EU target, shorter measurement windows. |
| `RSK-2` | EU provider onboarding requires business verification / long lead time | High | Med | Begin account applications during M1, in parallel, for the top 3 candidates — **before** final selection. |
| `RSK-3` | No EU candidate offers managed identity → CP-5 always rated 0.25 | Med | **High** | This is a *finding*, not a failure. Pre-plan the self-hosted Keycloak substitution and measure its operational debt. |
| `RSK-4` | No EU candidate offers a managed broker → same for CP-4 | Med | Med | As RSK-3; pre-plan the RabbitMQ substitution. |
| `RSK-5` | Cognito request-rate quotas, and per-request billing of client-credentials tokens (no free tier for M2M), make the S4 token scenario expensive or infeasible | Med | Med | Cache tokens in the harness; size S4 to a fixed, budgeted number of token requests; check quotas and current M2M prices at M1; document the constraint. |
| `RSK-6` | Scope creep: artifact quality pursued beyond research need | High | **High** | Size budget (`SRS.md` NFR-009); §9 enforced; every feature must trace to a requirement. |
| `RSK-7` | V0 perceived as a straw man in the defence | High | Med | `SRS.md` CR-011/CR-012 justification register; consider brief expert review. |
| `RSK-8` | Provider catalogue changes mid-study invalidate a measurement | Med | Med | Date every observation; pin versions (IR-04); re-verify at write-up. |
| `RSK-9` | Measurement noise exceeds the effect size in performance deltas | Med | Med | N repetitions (MR-07); report CIs; be prepared to report "no detectable difference" as a valid result. |
| `RSK-10` | Data Act interpretation contested by supervisors or legal reviewers | Med | Low | Confine claims to *operationalization*, not legal interpretation; verify all citations against the OJ text. |
| `RSK-11` | The pinned EKS Kubernetes version (IR-04) leaves standard support during the study, and the cluster fee rises from $0.10/h to $0.60/h | Med | Med | At M0, pin the newest version whose standard support lasts beyond P7; set a budget alert (`SRS.md` NFR-004). |
| `RSK-12` | Hidden baseline costs (EKS control plane, NAT gateway, load balancer) exceed the budget even when idle | Med | Med | Environments destroyed between campaigns (IR-03); cost model in §5.5 includes these line items; budget alert per target. |
| `RSK-13` | The supervisors see the switch of baseline from Azure to AWS as weakening the Art. 30(2)–(3) argument | Med | Low | §2.2 and §2.6 re-derive the argument with CP-2 as the de-facto-standard case; raise it explicitly at the next supervision meeting. |

---

# 11. Open Decisions

| ID | Decision | Needed by | Recommendation |
|---|---|---|---|
| `D-1` | Budget ceiling and funding source | **Before M1** | Blocking. Resolve first. |
| ~~`D-2`~~ | ~~Implementation language~~ | ~~M0~~ | **Closed 2026-09-25: Go.** |
| `D-3` | Number of EU targets: 1 (MUST) or 2 (SHOULD) | M1 | 2, contrast-selected per PS-10 — but only after budget confirmation. |
| `D-4` | Criticality weights for FE scoring | M1 | Derive from the case workflow's functional criticality; defend explicitly; sensitivity-test. |
| `D-5` | Reference workload profile values | M1 | Fix and freeze. |
| `D-6` | `N` repetitions per campaign | M2 | 5 minimum; raise if variance is high. |
| `D-7` | Benchmark application for the replication study | M1 | Select by coupling-class coverage, not popularity. |
| ~~`D-8`~~ | ~~Hyperscaler region~~ | ~~M0~~ | **Closed 2026-11-01: AWS `eu-central-1` (Frankfurt)**, held constant for the whole study. |
| `D-9` | Whether a composite portability index is reported at all | M6 | Only with sensitivity analysis (ER-06); the profile (ER-05) is the primary presentation. |
| ~~`D-10`~~ | ~~Hyperscaler baseline~~ | ~~M0~~ | **Closed 2026-11-01: AWS** (EKS, S3, RDS for PostgreSQL, SQS FIFO, Cognito). Aurora and Cognito Lambda triggers deliberately not used (`SRS.md` §6.2). Rationale in §2.6. |
| `D-11` | Whether the AWS European Sovereign Cloud is used as an additional, non-EU-sovereign comparison target | M1 | Only if budget allows and Cognito is available there; otherwise discussion only (§2.6). |

---

# 12. References

| ID | Reference |
|---|---|
| [R1] | Regulation (EU) 2023/2854 (Data Act), esp. Art. 2(37), Art. 2(40), Art. 23–31, Art. 35. *Exact article and recital numbering to be verified against the Official Journal text before submission.* |
| [R2] | ISO/IEC/IEEE 29148:2018 — Requirements engineering. |
| [R3] | ISO/IEC 25010:2023 — Systems and software quality models (portability, adaptability, replaceability, installability). |
| [R4] | Hevner, March, Park, Ram (2004): Design Science in Information Systems Research. |
| [R5] | Peffers et al. (2007): A Design Science Research Methodology for Information Systems Research. |
| [R6] | Opara-Martins, Sahandi, Tian (2016): Critical analysis of vendor lock-in and its impact on cloud computing migration. |
| [R7] | Wohlin et al. (2012): Experimentation in Software Engineering (validity threat taxonomy). |
| [R8] | Runeson & Höst (2009): Guidelines for conducting and reporting case study research in software engineering. |
| [R9] | k6 / Grafana Labs load testing documentation. |
| [R10] | OpenTofu, Helm, Kubernetes project documentation. |
| [R11] | AWS service documentation for the baseline: Amazon EKS, S3, RDS for PostgreSQL, SQS, Cognito. *Every capability statement to be dated at the time of observation.* |
| [R12] | AWS (2024): "Free data transfer out to internet when moving out of AWS", AWS News Blog; AWS EU Data Act Addendum. |

---

# Appendix A — Answers to the Supervisor Questions

| Supervisor question | Answer | Where |
|---|---|---|
| *"How is the microservice selection justified — representative of what?"* | Not representative of applications. The unit of analysis is the **coupling point**. The five primary CPs form a **maximum-variation purposive sample** across two dimensions (interface standardisation × state ownership), spanning fully-standardised/stateless to nominally-standardised/stateful. Each microservice is the minimal realistic carrier of exactly one CP. Generalisation is by **construct coverage**, not statistical sampling. | §2 |
| *"How should the EU provider be chosen?"* | By a **pre-registered three-stage procedure**: hard K.O. criteria → weighted scoring → timeboxed feasibility pilot. Criteria and weights fixed before evaluation; the full funnel including rejections is published. The *procedure* is the contribution; the provider is its output. Two providers, if chosen, must be architecturally contrasting. | §4 |
| *"Restrict to a specific area of the EU criteria."* | Scope is **Data Act Art. 30 only** — specifically the operationalization of **functional equivalence** (Art. 2(37), Art. 30(1)) and the **open-interfaces** clause (Art. 30(2)–(3)). Art. 23 provides the break-point framing. Contractual and fee provisions (Art. 25, 28, 29) are explicitly excluded as not measurable by a technical artifact. | §1.3, §5.1 |
| *"Which microservices should be built?"* | Exactly four: `identity-gateway` (CP-5), `case-service` (CP-3), `intake-service` (CP-2), `validation-worker` (CP-4); all on Kubernetes (CP-1). Each carries exactly one primary coupling point so that every break point is attributable without confounding. Full specification in [`SRS.md`](./SRS.md). | §2.5 |
| *(Added)* *Why AWS as the hyperscaler?* | Most common switching origin; it yields the full spread on the standardisation axis (open, de-facto, open-on-paper, proprietary); most mature IaC provider; comparable with prior work. Trade-offs (weaker public-sector vendor fit, AMQP case lost) are stated in the text. | §2.6 |
| *(Added)* *Is the abstraction-layer claim testable as drafted?* | Not as originally drafted — there was no control condition. The design now specifies **two variants (V0 native, V1 abstracted)** across targets, making the effectiveness of abstraction a measured difference rather than an assertion, and additionally yielding the **cost of portability** on the baseline. | §3.2 |
