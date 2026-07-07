#!/usr/bin/env bash
# setup-localstack.sh — provisions all LocalStack resources needed for e2e testing:
#   1. Creates an RDS PostgreSQL instance
#   2. Creates a local ECR repository
#   3. Builds the Lambda image (UBI9)
#   4. Pushes the image to local ECR
#   5. Creates the 3 DynamoDB status tables with streams
#   6. Creates (or updates) the Lambda function
#   7. Creates event source mappings (one per table)
#
# Prerequisites:
#   - LocalStack Pro running: ./hack/start-localstack.sh
#
# Environment variables:
#   LOCALSTACK_ENDPOINT  LocalStack endpoint (default: http://localhost:4566).
#   MC_NAME              Management cluster name prefix (default: mc01).
#   DOCKER_CMD           Container runtime (default: docker). Set to 'podman' if needed.

set -euo pipefail

ENDPOINT="${LOCALSTACK_ENDPOINT:-http://localhost:4566}"
REGION="us-east-1"
ACCOUNT="000000000000"
MC_NAME="${MC_NAME:-mc01}"
LAMBDA_NAME="dynamo-status-bridge"
REPO_NAME="dynamo-status-bridge"
DOCKER_CMD="${DOCKER_CMD:-docker}"

DB_INSTANCE_ID="statusbridge"
DB_NAME="statusbridge"
DB_USER="statusbridge"
DB_PASSWORD="statusbridge"

# LocalStack doesn't validate credentials but the AWS CLI requires them to be
# set. Inject dummy values so the CLI doesn't bail out before hitting the endpoint.
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
export AWS_DEFAULT_REGION="${REGION}"

AWS_ARGS="--endpoint-url=${ENDPOINT} --region=${REGION} --no-cli-pager"
awslocal() { aws ${AWS_ARGS} "$@"; }

# ---------------------------------------------------------------------------
# Wait for LocalStack
# ---------------------------------------------------------------------------
echo "==> Waiting for LocalStack to be ready..."
for i in $(seq 1 30); do
  if curl -sf "${ENDPOINT}/_localstack/health" | grep -q '"dynamodb"'; then
    echo "    LocalStack is ready."
    break
  fi
  if [[ "${i}" -eq 30 ]]; then
    echo "ERROR: LocalStack did not become ready in time."
    exit 1
  fi
  echo "    waiting... (${i}/30)"
  sleep 2
done

# ---------------------------------------------------------------------------
# 1. Create RDS PostgreSQL instance
# ---------------------------------------------------------------------------
echo "==> Creating RDS PostgreSQL instance: ${DB_INSTANCE_ID}"
awslocal rds create-db-instance \
  --db-instance-identifier "${DB_INSTANCE_ID}" \
  --engine postgres \
  --db-name "${DB_NAME}" \
  --master-username "${DB_USER}" \
  --master-user-password "${DB_PASSWORD}" \
  --db-instance-class db.t3.small \
  --allocated-storage 20 \
  2>/dev/null || echo "    (already exists)"

echo "==> Waiting for RDS instance to become available..."
awslocal rds wait db-instance-available --db-instance-identifier "${DB_INSTANCE_ID}"

RDS_HOST=$(awslocal rds describe-db-instances \
  --db-instance-identifier "${DB_INSTANCE_ID}" \
  --query 'DBInstances[0].Endpoint.Address' \
  --output text)
RDS_PORT=$(awslocal rds describe-db-instances \
  --db-instance-identifier "${DB_INSTANCE_ID}" \
  --query 'DBInstances[0].Endpoint.Port' \
  --output text)

echo "    RDS endpoint: ${RDS_HOST}:${RDS_PORT}"

# DSN for the Lambda container — reaches RDS inside LocalStack via the
# LocalStack internal hostname. USE_IAM_AUTH=false for local testing.
LAMBDA_POSTGRES_DSN="postgres://${DB_USER}:${DB_PASSWORD}@${RDS_HOST}:${RDS_PORT}/${DB_NAME}?sslmode=disable"

# DSN for the test process on the host — RDS port is exposed on localhost.
HOST_POSTGRES_DSN="postgres://${DB_USER}:${DB_PASSWORD}@localhost:${RDS_PORT}/${DB_NAME}?sslmode=disable"

# ---------------------------------------------------------------------------
# 2. Create ECR repository
# ---------------------------------------------------------------------------
echo "==> Creating ECR repository: ${REPO_NAME}"
REPO_URI=$(awslocal ecr create-repository \
  --repository-name "${REPO_NAME}" \
  --query 'repository.repositoryUri' \
  --output text 2>/dev/null \
  || awslocal ecr describe-repositories \
    --repository-names "${REPO_NAME}" \
    --query 'repositories[0].repositoryUri' \
    --output text)
echo "    Repository URI: ${REPO_URI}"

# ---------------------------------------------------------------------------
# 3. Build the Lambda image
# ---------------------------------------------------------------------------
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
echo "==> Building Lambda image from ${REPO_ROOT}"
"${DOCKER_CMD}" build -t "${REPO_NAME}:latest" "${REPO_ROOT}"

# ---------------------------------------------------------------------------
# 4. Login to local ECR and push image
# ---------------------------------------------------------------------------
# LocalStack's ECR hostname (*.localhost.localstack.cloud) resolves to 127.0.0.1
# via public DNS. Podman requires --tls-verify=false for self-signed/HTTP registries.
ECR_REGISTRY="${ACCOUNT}.dkr.ecr.${REGION}.localhost.localstack.cloud:4566"

# Detect whether the runtime supports --tls-verify (podman yes, docker no).
if "${DOCKER_CMD}" push --help 2>&1 | grep -q -- '--tls-verify'; then
  TLS_FLAG="--tls-verify=false"
else
  TLS_FLAG=""
fi

echo "==> Logging in to local ECR (${ECR_REGISTRY})"
awslocal ecr get-login-password \
  | "${DOCKER_CMD}" login \
      --username AWS \
      --password-stdin \
      ${TLS_FLAG} \
      "${ECR_REGISTRY}"

FULL_IMAGE_URI="${ECR_REGISTRY}/${REPO_NAME}:latest"
echo "==> Tagging and pushing (${FULL_IMAGE_URI})"
"${DOCKER_CMD}" tag "${REPO_NAME}:latest" "${FULL_IMAGE_URI}"
"${DOCKER_CMD}" push ${TLS_FLAG} "${FULL_IMAGE_URI}"

IMAGE_URI="${FULL_IMAGE_URI}"
echo "    Image URI: ${IMAGE_URI}"

# ---------------------------------------------------------------------------
# 5. Create DynamoDB status tables with streams
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
    2>/dev/null || echo "    (already exists)"

  awslocal dynamodb wait table-exists --table-name "${TABLE_NAME}"

  STREAM_ARN=$(awslocal dynamodb describe-table \
    --table-name "${TABLE_NAME}" \
    --query 'Table.LatestStreamArn' \
    --output text)
  STREAM_ARNS["${TABLE_TYPE}"]="${STREAM_ARN}"
  echo "    Stream ARN: ${STREAM_ARN}"
done

# ---------------------------------------------------------------------------
# 6. Create or update the Lambda function
# ---------------------------------------------------------------------------
echo "==> Creating Lambda function: ${LAMBDA_NAME}"
if awslocal lambda get-function --function-name "${LAMBDA_NAME}" >/dev/null 2>&1; then
  echo "    (already exists, updating code and config)"
  awslocal lambda update-function-code \
    --function-name "${LAMBDA_NAME}" \
    --image-uri "${IMAGE_URI}"
  awslocal lambda update-function-configuration \
    --function-name "${LAMBDA_NAME}" \
    --environment "Variables={USE_IAM_AUTH=false,POSTGRES_DSN=${LAMBDA_POSTGRES_DSN}}"
else
  awslocal lambda create-function \
    --function-name "${LAMBDA_NAME}" \
    --package-type Image \
    --code "ImageUri=${IMAGE_URI}" \
    --role "arn:aws:iam::${ACCOUNT}:role/lambda-role" \
    --timeout 60 \
    --memory-size 256 \
    --environment "Variables={USE_IAM_AUTH=false,POSTGRES_DSN=${LAMBDA_POSTGRES_DSN}}"
fi

echo "==> Waiting for Lambda to become active..."
awslocal lambda wait function-active-v2 --function-name "${LAMBDA_NAME}"
echo "    Lambda is active."

# ---------------------------------------------------------------------------
# 7. Create event source mappings
# ---------------------------------------------------------------------------
for TABLE_TYPE in "${TABLE_TYPES[@]}"; do
  STREAM_ARN="${STREAM_ARNS[${TABLE_TYPE}]}"
  echo "==> Creating ESM: ${TABLE_TYPE} -> ${LAMBDA_NAME}"
  awslocal lambda create-event-source-mapping \
    --function-name "${LAMBDA_NAME}" \
    --event-source-arn "${STREAM_ARN}" \
    --starting-position TRIM_HORIZON \
    --batch-size 10 \
    --function-response-types ReportBatchItemFailures \
    2>/dev/null || echo "    (ESM already exists)"
done

echo ""
echo "==> Setup complete!"
echo ""
echo "RDS endpoint (host):    ${RDS_HOST}:${RDS_PORT}"
echo ""
echo "Run e2e tests with:"
echo "  LOCALSTACK_ENDPOINT=${ENDPOINT} \\"
echo "  POSTGRES_DSN=${HOST_POSTGRES_DSN} \\"
echo "  MC_NAME=${MC_NAME} \\"
echo "  go test ./test/e2e/... -v -timeout 180s"
