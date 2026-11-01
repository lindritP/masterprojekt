# 0009. Use SeaweedFS as the local S3-compatible store

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS DR-012, DR-014, §7.3 (portable `DocumentStore` adapter)

## Context

DR-012 named MinIO for the local stack. The MinIO community edition went into maintenance
mode in December 2025 and its repository was archived in April 2026. Its images were later
reported removed from Docker Hub. A local S3 store without security patches or images is not
a sound base for a study meant to be reproducible (NFR-001).

The local store must support SigV4 presigned `GET`/`PUT`, presigned `POST` with a
`content-length-range` policy (FR-302) and, ideally, Object Lock (FR-309).

## Decision

Use SeaweedFS (`chrislusf/seaweedfs`, pinned tag) as the local S3-compatible store.
The bucket is created with Object Lock enabled by an init container.

## Consequences

- A probe on 2026-11-01 against SeaweedFS 4.48, using the AWS SDK for Go v2, showed the
  semantics the conformance suite will require:
  - presigned `PUT` rejects a wrong `Content-Type` (403) and an expired URL (403)
  - presigned `POST` accepts a valid upload (204), rejects an upload above
    `content-length-range` (400) and a wrong `Content-Type` (403)
  - a version under Object Lock compliance retention cannot be deleted (403)

  This is an exploratory check, not evidence. The port conformance suite at B2 is what
  establishes it formally (`PresignUpload_*`, `ApplyRetention_*`).
- The local stack is itself one more "S3-compatible" implementation. Its quirks are logged
  like any target's, not silently worked around.
