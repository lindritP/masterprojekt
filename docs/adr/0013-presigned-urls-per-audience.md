# 0013. Sign presigned URLs for the audience that will use them

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS FR-301, FR-306, FR-307, §9.3 (`CCSRA_STORAGE_PUBLIC_ENDPOINT`)

## Context

A presigned URL's signature covers its host. Citizens use the URL from outside the cluster;
validation-worker uses it from inside. On the local stack (and on `kind`) the object store has
different names on the two sides: `localhost:8333` for the host, `seaweedfs:8333` for the
containers. A URL signed for one side does not work on the other.

## Decision

intake-service builds two `DocumentStore` instances over the same bucket:

- **Public** signs the URLs returned through the public API (uploads, downloads), using
  `CCSRA_STORAGE_PUBLIC_ENDPOINT`.
- **Internal** serves direct calls (`Head`, `Delete`) and the URLs issued to
  validation-worker (`/internal/v1/objects/presign`), using `CCSRA_STORAGE_ENDPOINT`.

`CCSRA_STORAGE_PUBLIC_ENDPOINT` defaults to `CCSRA_STORAGE_ENDPOINT`.

## Consequences

- On a cloud target both endpoints are the provider's regional S3 endpoint, so the variable is
  simply left unset.
- Where a target needs it, it is a configuration line, counted in `ME_cfg`, not code.
- The port is unchanged: the split is wiring (`internal/wiring/storage`), not an adapter feature.
