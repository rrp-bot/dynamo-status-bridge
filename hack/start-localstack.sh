#!/usr/bin/env bash
# Starts a LocalStack Pro container with DynamoDB, DynamoDB Streams, Lambda, and ECR.
# Lambda container image support requires:
#   - LocalStack Pro (LOCALSTACK_AUTH_TOKEN)
#   - Docker socket mounted so LocalStack can spin up Lambda containers
#   - A shared Docker network so Lambda containers can reach Postgres

set -euo pipefail

CONTAINER_NAME="localstack-dynamo-status-bridge"
PORT="${LOCALSTACK_PORT:-4566}"
NETWORK="${DOCKER_NETWORK:-dsb-local}"

if [[ -z "${LOCALSTACK_AUTH_TOKEN:-}" ]]; then
  echo "ERROR: LOCALSTACK_AUTH_TOKEN is required for Lambda container image support (LocalStack Pro)"
  exit 1
fi

# Create shared network if it doesn't exist (Lambda containers + Postgres need to be on it)
docker network inspect "${NETWORK}" >/dev/null 2>&1 || docker network create "${NETWORK}"

docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true

docker run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK}" \
  -p "${PORT}:4566" \
  -e "LOCALSTACK_AUTH_TOKEN=${LOCALSTACK_AUTH_TOKEN}" \
  -e "SERVICES=dynamodb,dynamodbstreams,lambda,ecr" \
  -e "LAMBDA_DOCKER_NETWORK=${NETWORK}" \
  -e "DEBUG=0" \
  -v "/var/run/docker.sock:/var/run/docker.sock" \
  localstack/localstack-pro

echo "LocalStack Pro started on port ${PORT} (network: ${NETWORK})"
echo ""
echo "Next steps:"
echo "  1. Start Postgres:  ./hack/start-postgres.sh"
echo "  2. Setup LocalStack resources: ./hack/setup-localstack.sh"
echo "  3. Run e2e tests:   LOCALSTACK_ENDPOINT=http://localhost:${PORT} POSTGRES_DSN=... go test ./test/e2e/..."
