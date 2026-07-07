#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="postgres-dynamo-status-bridge"
PORT="${POSTGRES_PORT:-5432}"
PASSWORD="${POSTGRES_PASSWORD:-test}"

docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true

docker run -d \
  --name "${CONTAINER_NAME}" \
  -p "${PORT}:5432" \
  -e "POSTGRES_USER=test" \
  -e "POSTGRES_PASSWORD=${PASSWORD}" \
  -e "POSTGRES_DB=statusbridge_test" \
  postgres:16-alpine

echo "PostgreSQL started on port ${PORT}"
echo "Set POSTGRES_DSN=postgres://test:${PASSWORD}@localhost:${PORT}/statusbridge_test?sslmode=disable"
