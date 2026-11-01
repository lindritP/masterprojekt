# 0010. Parse and render PDFs with PDFium compiled to WebAssembly

- **Status:** Accepted
- **Date:** 2026-11-01
- **Affects:** SRS §4.5.1, FR-410, FR-412, FR-043, DR-001, DR-002, NFR-011

## Context

Validation step 5 renders page 1 and digests it. FR-410 requires the digest to be identical on
every target and on both `amd64` and `arm64`. SRS §4.5.1 asked for this to be verified early,
with a fallback to a transform without rasterisation if it failed.

Native PDF renderers (MuPDF, Poppler, native PDFium) need cgo. That makes the static
`scratch`/`distroless` images of DR-001 and the two-architecture builds of DR-002 harder, and
leaves room for differences from compiler flags, SIMD paths and system fonts.

## Decision

Use `github.com/klippa-app/go-pdfium` (v1.21.4) in WebAssembly mode: PDFium compiled to
WASM, embedded in the binary and executed by wazero (v1.12.0). No cgo. The module gets no file
system mount; documents are passed as bytes.

The rendered bitmap (72 DPI) is reduced to a 64×64 grayscale matrix by integer box averaging
and integer BT.601 luma, then hashed with SHA-256. No floating point happens outside the WASM
module.

## Consequences

- **Spike result (2026-11-01):** all 8 corpus fixtures produce the expected outcomes and
  identical content digests on darwin/arm64, linux/arm64 and linux/amd64 (amd64 under
  emulation on Apple Silicon). The corpus generator also produced byte-identical files on all
  three. The fallback transform is not needed.
- CI repeats the check on native `ubuntu-24.04` and `ubuntu-24.04-arm` runners (FR-043).
- The binary grows by roughly 5 MB for the embedded module; images stay well under NFR-011.
- Rendering in WASM is about 2× slower than native PDFium (per the library's own benchmarks).
  The pipeline is meant to be CPU-bound (§4.5.1), so this only shifts absolute numbers, equally
  on every target.
- Upgrading go-pdfium changes the embedded PDFium build and may change digests. Upgrades need
  a new ADR and a deliberate `-update` of `test/corpus/expected.json` (FR-412).
