#!/usr/bin/env bash
# Starts a LocalStack container with DynamoDB + DynamoDB Streams for integration testing.
# Mirrors the pattern from kube-applier-aws/hack/start-localstack.sh.

set -euo pipefail

CONTAINER_NAME="localstack-dynamo-status-bridge"
PORT="${LOCALSTACK_PORT:-4566}"

if [[ -n "${LOCALSTACK_AUTH_TOKEN:-}" ]]; then
  IMAGE="localstack/localstack-pro"
  AUTH_ARGS=(-e "LOCALSTACK_AUTH_TOKEN=${LOCALSTACK_AUTH_TOKEN}")
else
  IMAGE="localstack/localstack"
  AUTH_ARGS=()
fi

docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true

docker run -d \
  --name "${CONTAINER_NAME}" \
  -p "${PORT}:4566" \
  -e "SERVICES=dynamodb,dynamodbstreams" \
  -e "DEBUG=0" \
  "${AUTH_ARGS[@]}" \
  "${IMAGE}"

echo "LocalStack started on port ${PORT}"
echo "Set LOCALSTACK_ENDPOINT=http://localhost:${PORT} before running integration tests"
