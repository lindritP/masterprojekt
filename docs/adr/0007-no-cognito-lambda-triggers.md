# 0007. Carry roles in `cognito:groups`, without Lambda triggers

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS CR-007, FR-103, §9.3 (`CCSRA_CLAIM_ROLES`); Research-Design §9

## Context

The usual way to put custom claims into Cognito access tokens is a pre-token-generation
trigger. It runs as an AWS Lambda function and needs the Essentials tier. Research-Design §9
excludes serverless/FaaS as a coupling point.

## Decision

V0 uses no Cognito Lambda triggers. Roles are Cognito user-pool groups, delivered in the
native `cognito:groups` claim. The gateway reads roles from the claim named by
`CCSRA_CLAIM_ROLES`, which is `cognito:groups` on the AWS baseline and `roles` on Keycloak.

## Consequences

- FaaS does not enter the study through the identity provider.
- Claim mapping stays a configuration difference plus one function in `identity-gateway`
  (FR-103), which keeps the CP-5 diff small and attributable.
- Cognito access tokens carry `client_id` rather than `aud` `[verify at B5]`. If confirmed,
  audience validation (FR-101) differs between Cognito and Keycloak, and that difference is
  logged as a CP-5 break point.
