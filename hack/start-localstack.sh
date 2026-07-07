#!/usr/bin/env bash
# start-localstack.sh — starts a LocalStack Pro container with DynamoDB,
# DynamoDB Streams, Lambda, ECR, and RDS enabled.
#
# Lambda container image support and RDS require LocalStack Pro.
# The Docker/Podman socket is mounted so LocalStack can spin up Lambda
# containers. A shared network is created so Lambda containers can reach
# the RDS instance running inside LocalStack.
#
# Usage: ./hack/start-localstack.sh
# Stop with: ${DOCKER_CMD} stop localstack-dynamo-status-bridge
#
# Environment variables:
#   LOCALSTACK_AUTH_TOKEN  Required. LocalStack Pro auth token.
#   LOCALSTACK_PORT        LocalStack port (default: 4566).
#   DOCKER_CMD             Container runtime (default: docker). Set to 'podman' if needed.
#   DOCKER_SOCKET          Socket path. Defaults to the podman rootless socket when
#                          DOCKER_CMD=podman (/run/user/UID/podman/podman.sock),
#                          or /var/run/docker.sock for docker.
#   DOCKER_NETWORK         Shared network name (default: dsb-local).

set -euo pipefail

CONTAINER_NAME="localstack-dynamo-status-bridge"
PORT="${LOCALSTACK_PORT:-4566}"
DOCKER_CMD="${DOCKER_CMD:-docker}"
NETWORK="${DOCKER_NETWORK:-dsb-local}"

# Default socket: rootless podman socket on Fedora when DOCKER_CMD=podman,
# otherwise the standard docker socket.
if [[ "${DOCKER_CMD}" == "podman" ]]; then
  DOCKER_SOCKET="${DOCKER_SOCKET:-/run/user/$(id -u)/podman/podman.sock}"
else
  DOCKER_SOCKET="${DOCKER_SOCKET:-/var/run/docker.sock}"
fi

if [[ -z "${LOCALSTACK_AUTH_TOKEN:-}" ]]; then
  echo "ERROR: LOCALSTACK_AUTH_TOKEN is required (Lambda + RDS support needs LocalStack Pro)."
  echo "       Export LOCALSTACK_AUTH_TOKEN=<your-token> and re-run."
  exit 1
fi

# Ensure the podman socket is running (no-op if already active).
if [[ "${DOCKER_CMD}" == "podman" ]]; then
  systemctl --user enable --now podman.socket 2>/dev/null || true
fi

# Create shared network if it doesn't exist.
"${DOCKER_CMD}" network inspect "${NETWORK}" >/dev/null 2>&1 \
  || "${DOCKER_CMD}" network create "${NETWORK}"

# Remove any stale container with the same name.
"${DOCKER_CMD}" rm -f "${CONTAINER_NAME}" 2>/dev/null || true

echo "Starting localstack/localstack-pro on port ${PORT} (network: ${NETWORK}) ..."
echo "  Socket: ${DOCKER_SOCKET}"
"${DOCKER_CMD}" run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK}" \
  -p "${PORT}:4566" \
  -p "4510-4560:4510-4560" \
  -e "LOCALSTACK_AUTH_TOKEN=${LOCALSTACK_AUTH_TOKEN}" \
  -e "SERVICES=dynamodb,dynamodbstreams,lambda,ecr,rds" \
  -e "LAMBDA_DOCKER_NETWORK=${NETWORK}" \
  -e "DOCKER_HOST=unix:///var/run/docker.sock" \
  -e "LAMBDA_REMOVE_CONTAINERS=true" \
  -e "DEBUG=0" \
  --user 0 \
  --privileged \
  -v "${DOCKER_SOCKET}:/var/run/docker.sock:Z" \
  localstack/localstack-pro

echo "LocalStack Pro container '${CONTAINER_NAME}' started."
echo "  Socket mounted: ${DOCKER_SOCKET} -> /var/run/docker.sock"
echo "Set LOCALSTACK_ENDPOINT=http://localhost:${PORT} before running tests."
echo "Stop with: ${DOCKER_CMD} stop ${CONTAINER_NAME}"
