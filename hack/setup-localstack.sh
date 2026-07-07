#!/usr/bin/env bash
# Sets up all LocalStack resources needed for e2e testing:
#   1. Creates a local ECR repository
#   2. Builds the Lambda Docker image (UBI9)
#   3. Pushes the image to local ECR
#   4. Creates the 3 DynamoDB status tables with streams
#   5. Creates the Lambda function pointing at the ECR image
#   6. Creates event source mappings (one per table)
#
# Prerequisites:
#   - LocalStack Pro running (./hack/start-localstack.sh)
#   - Postgres running (./hack/start-postgres.sh)
#   - LOCALSTACK_ENDPOINT set (default: http://localhost:4566)
#   - LAMBDA_POSTGRES_DSN set (using the container hostname, e.g. postgres://test:test@postgres-dynamo-status-bridge:5432/statusbridge_test?sslmode=disable)

set -euo pipefail

ENDPOINT="${LOCALSTACK_ENDPOINT:-http://localhost:4566}"
REGION="us-east-1"
ACCOUNT="000000000000"
MC_NAME="${MC_NAME:-mc01}"
LAMBDA_NAME="dynamo-status-bridge"
REPO_NAME="dynamo-status-bridge"

# DSN the Lambda will use — must use the Docker network container hostname for Postgres
LAMBDA_POSTGRES_DSN="${LAMBDA_POSTGRES_DSN:-postgres://test:test@postgres-dynamo-status-bridge:5432/statusbridge_test?sslmode=disable}"

AWS_ARGS="--endpoint-url=${ENDPOINT} --region=${REGION}"
# shellcheck disable=SC2086
awslocal() { aws ${AWS_ARGS} --no-cli-pager "$@"; }

echo "==> Waiting for LocalStack to be ready..."
for i in $(seq 1 30); do
  if curl -sf "${ENDPOINT}/_localstack/health" | grep -q '"dynamodb": "available"'; then
    break
  fi
  echo "    waiting... (${i}/30)"
  sleep 2
done

# ---------------------------------------------------------------------------
# 1. Create ECR repository
# ---------------------------------------------------------------------------
echo "==> Creating ECR repository: ${REPO_NAME}"
REPO_URI=$(awslocal ecr create-repository \
  --repository-name "${REPO_NAME}" \
  --query 'repository.repositoryUri' \
  --output text 2>/dev/null || \
  awslocal ecr describe-repositories \
    --repository-names "${REPO_NAME}" \
    --query 'repositories[0].repositoryUri' \
    --output text)
echo "    Repository URI: ${REPO_URI}"

# ---------------------------------------------------------------------------
# 2. Build the Lambda image
# ---------------------------------------------------------------------------
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
echo "==> Building Lambda image from ${REPO_ROOT}"
docker build -t "${REPO_NAME}:latest" "${REPO_ROOT}"

# ---------------------------------------------------------------------------
# 3. Push to local ECR (no docker login needed for LocalStack)
# ---------------------------------------------------------------------------
echo "==> Tagging and pushing to local ECR"
docker tag "${REPO_NAME}:latest" "${REPO_URI}:latest"
docker push "${REPO_URI}:latest"

IMAGE_URI="${REPO_URI}:latest"
echo "    Image URI: ${IMAGE_URI}"

# ---------------------------------------------------------------------------
# 4. Create DynamoDB status tables with streams
# ---------------------------------------------------------------------------
TABLE_TYPES=("applydesires" "deletedesires" "readdesires")
declare -A STREAM_ARNS

for TABLE_TYPE in "${TABLE_TYPES[@]}"; do
  TABLE_NAME="${MC_NAME}-status-${TABLE_TYPE}"
  echo "==> Creating DynamoDB table: ${TABLE_NAME}"

  awslocal dynamodb create-table \
    --table-name "${TABLE_NAME}" \
    --attribute-definitions AttributeName=documentID,AttributeType=S \
    --key-schema AttributeName=documentID,KeyType=HASH \
    --billing-mode PAY_PER_REQUEST \
    --stream-specification StreamEnabled=true,StreamViewType=NEW_AND_OLD_IMAGES \
    2>/dev/null || echo "    (table already exists)"

  # Wait for table to be active
  awslocal dynamodb wait table-exists --table-name "${TABLE_NAME}"

  STREAM_ARN=$(awslocal dynamodb describe-table \
    --table-name "${TABLE_NAME}" \
    --query 'Table.LatestStreamArn' \
    --output text)
  STREAM_ARNS["${TABLE_TYPE}"]="${STREAM_ARN}"
  echo "    Stream ARN: ${STREAM_ARN}"
done

# ---------------------------------------------------------------------------
# 5. Create the Lambda function
# ---------------------------------------------------------------------------
echo "==> Creating Lambda function: ${LAMBDA_NAME}"
awslocal lambda create-function \
  --function-name "${LAMBDA_NAME}" \
  --package-type Image \
  --code "ImageUri=${IMAGE_URI}" \
  --role "arn:aws:iam::${ACCOUNT}:role/lambda-role" \
  --timeout 60 \
  --memory-size 256 \
  --environment "Variables={USE_IAM_AUTH=false,POSTGRES_DSN=${LAMBDA_POSTGRES_DSN}}" \
  2>/dev/null || \
awslocal lambda update-function-code \
  --function-name "${LAMBDA_NAME}" \
  --image-uri "${IMAGE_URI}"

echo "==> Waiting for Lambda to become active..."
awslocal lambda wait function-active-v2 --function-name "${LAMBDA_NAME}"
echo "    Lambda is active"

# ---------------------------------------------------------------------------
# 6. Create event source mappings
# ---------------------------------------------------------------------------
for TABLE_TYPE in "${TABLE_TYPES[@]}"; do
  STREAM_ARN="${STREAM_ARNS[${TABLE_TYPE}]}"
  echo "==> Creating ESM for ${TABLE_TYPE} -> ${STREAM_ARN}"
  awslocal lambda create-event-source-mapping \
    --function-name "${LAMBDA_NAME}" \
    --event-source-arn "${STREAM_ARN}" \
    --starting-position TRIM_HORIZON \
    --batch-size 10 \
    --function-response-types ReportBatchItemFailures \
    2>/dev/null || echo "    (ESM may already exist)"
done

echo ""
echo "==> Setup complete!"
echo ""
echo "Run e2e tests with:"
echo "  LOCALSTACK_ENDPOINT=${ENDPOINT} \\"
echo "  POSTGRES_DSN=postgres://test:test@localhost:5432/statusbridge_test?sslmode=disable \\"
echo "  MC_NAME=${MC_NAME} \\"
echo "  go test ./test/e2e/... -v -timeout 120s"
