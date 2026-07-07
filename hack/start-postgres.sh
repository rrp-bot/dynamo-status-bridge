#!/usr/bin/env bash
# Starts a local PostgreSQL container for integration/e2e testing.
# Joins the same Docker network as LocalStack so Lambda containers can reach it.

set -euo pipefail

CONTAINER_NAME="postgres-dynamo-status-bridge"
PORT="${POSTGRES_PORT:-5432}"
PASSWORD="${POSTGRES_PASSWORD:-test}"
NETWORK="${DOCKER_NETWORK:-dsb-local}"

# Create shared network if it doesn't exist
docker network inspect "${NETWORK}" >/dev/null 2>&1 || docker network create "${NETWORK}"

docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true

docker run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK}" \
  -p "${PORT}:5432" \
  -e "POSTGRES_USER=test" \
  -e "POSTGRES_PASSWORD=${PASSWORD}" \
  -e "POSTGRES_DB=statusbridge_test" \
  postgres:16-alpine

echo "PostgreSQL started on port ${PORT} (network: ${NETWORK})"
echo "Container hostname on network: ${CONTAINER_NAME}"
echo ""
echo "For integration tests (direct handler):  POSTGRES_DSN=postgres://test:${PASSWORD}@localhost:${PORT}/statusbridge_test?sslmode=disable"
echo "For e2e tests (via Lambda container):    LAMBDA_POSTGRES_DSN=postgres://test:${PASSWORD}@${CONTAINER_NAME}:5432/statusbridge_test?sslmode=disable"
