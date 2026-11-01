#!/bin/sh
# Writes the runtime configuration of the demo UI from the environment.
set -eu
cat > /usr/share/nginx/html/config.js <<CONFIG
window.CCSRA_CONFIG = {
  apiBaseUrl: "${CCSRA_API_BASE_URL:-http://localhost:8080/api/v1}",
  issuer: "${CCSRA_OIDC_ISSUER:-http://localhost:8180/realms/ccsra}",
  clientId: "${CCSRA_OIDC_CLIENT_ID:-ccsra-web}",
};
CONFIG
