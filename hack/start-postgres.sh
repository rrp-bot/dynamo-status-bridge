#!/usr/bin/env bash
# start-postgres.sh — starts a local PostgreSQL container for integration and
# e2e testing. Joins the same shared network as LocalStack so Lambda containers
# can reach Postgres by hostname.
#
# Usage: ./hack/start-postgres.sh
# Stop with: ${DOCKER_CMD} stop postgres-dynamo-status-bridge
#
# Environment variables:
#   POSTGRES_PORT    Host port to expose (default: 5432).
#   POSTGRES_PASSWORD  Postgres password (default: test).
#   DOCKER_CMD       Container runtime (default: docker). Set to 'podman' if needed.
#   DOCKER_NETWORK   Shared network name (default: dsb-local).

set -euo pipefail

CONTAINER_NAME="postgres-dynamo-status-bridge"
PORT="${POSTGRES_PORT:-5432}"
PASSWORD="${POSTGRES_PASSWORD:-test}"
DOCKER_CMD="${DOCKER_CMD:-docker}"
NETWORK="${DOCKER_NETWORK:-dsb-local}"

# Create shared network if it doesn't exist.
"${DOCKER_CMD}" network inspect "${NETWORK}" >/dev/null 2>&1 \
  || "${DOCKER_CMD}" network create "${NETWORK}"

# Remove any stale container with the same name.
"${DOCKER_CMD}" rm -f "${CONTAINER_NAME}" 2>/dev/null || true

echo "Starting postgres:16-alpine on port ${PORT} (network: ${NETWORK}) ..."
"${DOCKER_CMD}" run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK}" \
  -p "${PORT}:5432" \
  -e "POSTGRES_USER=test" \
  -e "POSTGRES_PASSWORD=${PASSWORD}" \
  -e "POSTGRES_DB=statusbridge_test" \
  postgres:16-alpine

echo "PostgreSQL container '${CONTAINER_NAME}' started."
echo "Container hostname on the shared network: ${CONTAINER_NAME}"
echo ""
echo "For integration tests (direct):  POSTGRES_DSN=postgres://test:${PASSWORD}@localhost:${PORT}/statusbridge_test?sslmode=disable"
echo "For e2e tests (via Lambda):      LAMBDA_POSTGRES_DSN=postgres://test:${PASSWORD}@${CONTAINER_NAME}:5432/statusbridge_test?sslmode=disable"
echo "Stop with: ${DOCKER_CMD} stop ${CONTAINER_NAME}"
