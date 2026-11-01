#!/usr/bin/env bash
# Import-boundary check (SRS AR-004, FR-003, FR-004, FR-005).
#
# These rules are measurement requirements, not style: if a provider SDK leaks
# into a second service, a break point can no longer be attributed to exactly
# one coupling class (SRS §2.4).
set -euo pipefail
cd "$(dirname "$0")/.."

# Package-path patterns per capability class.
STORAGE='^github\.com/aws/aws-sdk-go-v2/service/s3|^github\.com/minio/|^github\.com/seaweedfs/'
DATABASE='^database/sql|^github\.com/jackc/|^github\.com/lib/pq'
BROKER='^github\.com/aws/aws-sdk-go-v2/service/sqs|^github\.com/Azure/go-amqp|^github\.com/rabbitmq/'
IDENTITY='^github\.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider|^github\.com/coreos/go-oidc|^github\.com/lestrrat-go/jwx|^github\.com/golang-jwt/'
ANY_PROVIDER="$STORAGE|$DATABASE|$BROKER|$IDENTITY|^github\.com/aws/|/internal/adapters"

failed=0

# check MODULE PACKAGE FORBIDDEN RULE
check() {
  local mod=$1 pkg=$2 forbidden=$3 rule=$4 deps hits
  [ -d "$mod/${pkg%/...}" ] || return 0
  deps=$(cd "$mod" && go list -deps "$pkg")
  hits=$(grep -E "$forbidden" <<<"$deps" || true)
  if [ -n "$hits" ]; then
    echo "FAIL [$rule] $mod/${pkg#./} depends on:"
    sed 's/^/    /' <<<"$hits"
    failed=1
  fi
}

for mod in app/v0 app/v1; do
  check "$mod" ./cmd/identity-gateway  "$STORAGE|$DATABASE|$BROKER"  "FR-002/FR-003 gateway links no storage, DB or broker"
  check "$mod" ./cmd/case-service      "$STORAGE|$IDENTITY"          "FR-003 case-service links no storage or IdP SDK"
  check "$mod" ./cmd/intake-service    "$DATABASE|$IDENTITY"         "FR-005 intake-service holds no DB connection"
  check "$mod" ./cmd/validation-worker "$STORAGE|$DATABASE|$IDENTITY" "FR-004/FR-005 worker uses presigned URLs only, no DB"
done

check app/v1 ./internal/domain/... "$ANY_PROVIDER" "AR-004 domain imports no adapter or provider SDK"
check app/v1 ./internal/ports/...  "$ANY_PROVIDER" "AR-002/AR-004 ports import no adapter or provider SDK"

if [ "$failed" -ne 0 ]; then
  exit 1
fi
echo "import boundaries OK"
