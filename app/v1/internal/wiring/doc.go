// Package wiring documents the adapter-selection packages below it. Each port
// has its own package, so a service links only the SDKs of the adapters it can
// select (FR-003; scripts/check-boundaries.sh checks this per binary).
// Selection is configuration only: CCSRA_ADAPTER_* at deployment time, no code
// change and no recompilation (AR-003); an unknown name stops startup (AR-009).
package wiring
