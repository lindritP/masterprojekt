// Runtime configuration of the demo UI. The container overwrites this file
// from environment variables; these defaults fit the local stack (make dev).
window.CCSRA_CONFIG = {
  apiBaseUrl: "http://localhost:8080/api/v1",
  issuer: "http://localhost:8180/realms/ccsra",
  clientId: "ccsra-web",
};
