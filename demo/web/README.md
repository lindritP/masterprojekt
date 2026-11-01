# Demo UI

A clickable front end for presentations: citizens create applications, upload PDFs and follow
the automatic validation; caseworkers review and decide. It is **not part of the measured
artifact** and is never deployed to a cloud target (ADR 0014).

```bash
make demo        # from the repository root: stack + services + this UI on http://localhost:3000
```

Log in with a test account of the local realm: `citizen-alice` or `citizen-bob` (applicants),
`caseworker-carol` (caseworker). The passwords are in `deploy/local/keycloak/ccsra-realm.json`.
For a clean demo without the E2E suite's cases, run `make stack-reset` first.

## Developing

```bash
npm install
npm run dev      # Vite on http://localhost:3000, against the API started by `make dev`
npm run build    # type-check and build into dist/
```

Runtime settings (API base URL, OIDC issuer, client id) come from `public/config.js`; the
container writes that file from `CCSRA_API_BASE_URL`, `CCSRA_OIDC_ISSUER` and
`CCSRA_OIDC_CLIENT_ID`.

## How it works

- Login: OIDC authorization code + PKCE with `oidc-client-ts`, so any compliant issuer works —
  Keycloak locally, Cognito on AWS.
- Every call goes to the public API (`src/api.ts`). Uploads go straight from the browser to
  object storage through the presigned POST form the API returns; the UI never handles bytes
  on a server.
- While a document is being validated, the case view polls every 1.5 s.
