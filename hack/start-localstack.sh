#!/usr/bin/env bash
# start-localstack.sh — starts a LocalStack Pro container with DynamoDB,
# DynamoDB Streams, Lambda, and ECR enabled.
#
# Lambda container image support requires LocalStack Pro and the Docker/Podman
# socket mounted so LocalStack can spin up Lambda containers.
# A shared network is created so Lambda containers can reach the Postgres
# container by hostname.
#
# Usage: ./hack/start-localstack.sh
# Stop with: ${DOCKER_CMD} stop localstack-dynamo-status-bridge
#
# Environment variables:
#   LOCALSTACK_AUTH_TOKEN  Required. LocalStack Pro auth token.
#   LOCALSTACK_PORT        LocalStack port (default: 4566).
#   DOCKER_CMD             Container runtime (default: docker). Set to 'podman' if needed.
#   DOCKER_SOCKET          Socket path (default: /var/run/docker.sock).
#                          For rootless podman on Fedora: /run/user/$(id -u)/podman/podman.sock
#   DOCKER_NETWORK         Shared network name (default: dsb-local).

set -euo pipefail

CONTAINER_NAME="localstack-dynamo-status-bridge"
PORT="${LOCALSTACK_PORT:-4566}"
DOCKER_CMD="${DOCKER_CMD:-docker}"
DOCKER_SOCKET="${DOCKER_SOCKET:-/var/run/docker.sock}"
NETWORK="${DOCKER_NETWORK:-dsb-local}"

if [[ -z "${LOCALSTACK_AUTH_TOKEN:-}" ]]; then
  echo "ERROR: LOCALSTACK_AUTH_TOKEN is required (Lambda container image support needs LocalStack Pro)."
  echo "       Export LOCALSTACK_AUTH_TOKEN=<your-token> and re-run."
  exit 1
fi

# Create shared network if it doesn't exist.
"${DOCKER_CMD}" network inspect "${NETWORK}" >/dev/null 2>&1 \
  || "${DOCKER_CMD}" network create "${NETWORK}"

# Remove any stale container with the same name.
"${DOCKER_CMD}" rm -f "${CONTAINER_NAME}" 2>/dev/null || true

echo "Starting localstack/localstack-pro on port ${PORT} (network: ${NETWORK}) ..."
"${DOCKER_CMD}" run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK}" \
  -p "${PORT}:4566" \
  -e "LOCALSTACK_AUTH_TOKEN=${LOCALSTACK_AUTH_TOKEN}" \
  -e "SERVICES=dynamodb,dynamodbstreams,lambda,ecr" \
  -e "LAMBDA_DOCKER_NETWORK=${NETWORK}" \
  -e "DEBUG=0" \
  -v "${DOCKER_SOCKET}:/var/run/docker.sock" \
  localstack/localstack-pro

echo "LocalStack Pro container '${CONTAINER_NAME}' started."
echo "Set LOCALSTACK_ENDPOINT=http://localhost:${PORT} before running tests."
echo "Stop with: ${DOCKER_CMD} stop ${CONTAINER_NAME}"
