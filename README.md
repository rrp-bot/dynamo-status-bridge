# dynamo-status-bridge

A Go AWS Lambda that bridges kube-applier-aws DynamoDB status tables into RDS
PostgreSQL. It consumes DynamoDB Streams from the three status tables
(`applydesires`, `deletedesires`, `readdesires`) across all management clusters
and writes the data into three corresponding PostgreSQL tables using upsert
semantics. Hard deletes are propagated on DynamoDB `REMOVE` events.

## Architecture

```
kube-applier-aws (Management Cluster)
  │
  ├── {mc}-status-applydesires   (DynamoDB + Stream)
  ├── {mc}-status-deletedesires  (DynamoDB + Stream)
  └── {mc}-status-readdesires    (DynamoDB + Stream)
          │
          │  Event Source Mappings (one per table per MC)
          ▼
  dynamo-status-bridge (Lambda, container image)
          │
          │  pgx/v5, IAM auth in production
          ▼
  RDS PostgreSQL
  ├── apply_desire_statuses
  ├── delete_desire_statuses
  └── read_desire_statuses
```

- **Desire type** is inferred from the stream ARN suffix (`applydesires` /
  `deletedesires` / `readdesires`) — no per-table Lambda required.
- **Multiple management clusters** are supported; each MC gets its own set of
  ESMs pointing at the same Lambda function.
- **Partial batch failures** are reported via `BatchItemFailures` so only
  failed records are retried.
- **Batch deduplication**: within a batch, only the newest record per
  `documentID` is written to Postgres. DynamoDB guarantees oldest-to-newest
  ordering within a shard, so the last record wins. If the newest record fails,
  all superseded predecessors are also reported as failures.
- **IAM authentication** is used for RDS in production. Plain DSN
  (`USE_IAM_AUTH=false` + `POSTGRES_DSN`) is used for local testing.

## Repository layout

```
cmd/lambda/          Lambda entrypoint
internal/
  config/            Environment variable loading
  db/
    schema/          SQL migrations (embedded, applied on cold start)
    client.go        pgxpool with IAM token refresh on BeforeConnect
    writer.go        Upsert / delete methods per desire type
  decoder/           DynamoDB stream image → typed structs
  handler/           Batch dispatch and partial failure reporting
test/
  integration/       Direct handler tests (no Lambda, plain Postgres)
  e2e/               Full Lambda tests via LocalStack Pro + RDS
hack/
  start-localstack.sh   Start LocalStack Pro container
  setup-localstack.sh   Provision all LocalStack resources
  start-postgres.sh     Start plain Postgres for integration tests
Dockerfile           UBI9 builder → ubi-minimal runtime
Makefile
terraform/           (see rosa-hyperfleet for production Terraform)
```

## PostgreSQL schema

Three tables, all with `PRIMARY KEY (document_id, management_cluster)`:

| Table | Source DynamoDB table |
|---|---|
| `apply_desire_statuses` | `{mc}-status-applydesires` |
| `delete_desire_statuses` | `{mc}-status-deletedesires` |
| `read_desire_statuses` | `{mc}-status-readdesires` |

`read_desire_statuses` additionally has a `kube_content JSONB` column that
holds the full mirrored Kubernetes object (`status_kubeContent` in DynamoDB),
or `NULL` when the object does not exist on the management cluster.

The schema is embedded in the binary and applied automatically on cold start
(`CREATE TABLE IF NOT EXISTS` — idempotent).

## Environment variables

### Production (IAM auth)

| Variable | Required | Default | Description |
|---|---|---|---|
| `RDS_HOST` | yes | — | RDS instance hostname |
| `RDS_PORT` | no | `5432` | RDS port |
| `RDS_DB` | no | `statusbridge` | Database name |
| `RDS_USER` | no | `statusbridge` | PostgreSQL username (must have `rds_iam` granted) |
| `AWS_REGION` | yes | — | AWS region (used for IAM token generation) |

### Local / testing (plain DSN)

| Variable | Required | Description |
|---|---|---|
| `USE_IAM_AUTH` | no | Set to `false` to use a plain DSN instead of IAM auth |
| `POSTGRES_DSN` | yes (when `USE_IAM_AUTH=false`) | Full PostgreSQL connection string |

## Building

Requires Go 1.26+ or the UBI9 builder image (see `make gosum` below).

```bash
# Local binary (for development)
make build

# Container image
DOCKER_CMD=podman make docker-build

# Generate go.sum via the builder image (if you don't have Go locally)
DOCKER_CMD=podman make gosum
```

## Testing

There are two test suites with different prerequisites:

### Unit / integration tests

These test the handler directly against a plain Postgres container. No
LocalStack or Lambda involved.

**Prerequisites:** Postgres running (see below).

```bash
# Start Postgres
DOCKER_CMD=podman make postgres
# Prints the DSN to use, e.g.:
#   POSTGRES_DSN=postgres://test:test@localhost:5432/statusbridge_test?sslmode=disable

# Run all integration tests
POSTGRES_DSN=postgres://test:test@localhost:5432/statusbridge_test?sslmode=disable \
  make integration-test

# Or run a specific test
POSTGRES_DSN=postgres://test:test@localhost:5432/statusbridge_test?sslmode=disable \
  go test ./test/integration/... -v -run TestIntegration_BatchDedup
```

The integration tests cover:
- INSERT / MODIFY → upsert for all three desire types
- REMOVE → hard delete
- `kube_content` populated for read desires
- Bad stream ARN → `BatchItemFailure` (no panic, rest of batch continues)
- Batch deduplication: newest record per `documentID` wins

### End-to-end tests (LocalStack Pro)

These run the Lambda as a real container image inside LocalStack Pro, with RDS
PostgreSQL provisioned inside LocalStack. Require a LocalStack Pro auth token.

**Prerequisites:**

- LocalStack Pro auth token (`LOCALSTACK_AUTH_TOKEN`)
- Podman or Docker
- `*.localhost.localstack.cloud` resolving to `127.0.0.1` (add to `/etc/hosts`
  if your DNS doesn't resolve it)

**Step 1 — Start LocalStack:**

```bash
LOCALSTACK_AUTH_TOKEN=<your-token> DOCKER_CMD=podman make localstack
```

This starts `localstack/localstack-pro` with DynamoDB, DynamoDB Streams,
Lambda, ECR, and RDS enabled. The Lambda container network is configured so
Lambda containers can reach the RDS instance inside LocalStack.

**Step 2 — Provision resources:**

```bash
MC_NAME=mc01 DOCKER_CMD=podman make setup-localstack
```

This script:
1. Creates an RDS PostgreSQL instance inside LocalStack
2. Creates an ECR repository
3. Builds the Lambda container image (UBI9)
4. Pushes the image to local ECR
5. Creates the three DynamoDB status tables with streams
6. Creates (or updates) the Lambda function
7. Creates one Event Source Mapping per table

At the end it prints the `POSTGRES_DSN` and the exact `go test` command to run.

**Step 3 — Run the e2e tests:**

```bash
LOCALSTACK_ENDPOINT=http://localhost:4566 \
  POSTGRES_DSN=postgres://statusbridge:statusbridge@localhost:<rds-port>/statusbridge?sslmode=disable \
  MC_NAME=mc01 \
  make e2e
```

The RDS port is printed by `setup-localstack.sh` (LocalStack assigns a port in
the `4510–4559` range).

**Re-running after code changes:**

Re-run `make setup-localstack` — it detects an existing Lambda function and
calls `update-function-code` + `update-function-configuration` instead of
creating from scratch.

**Stopping LocalStack:**

```bash
podman stop localstack-dynamo-status-bridge
podman rm localstack-dynamo-status-bridge
```

### Makefile reference

| Target | Description |
|---|---|
| `make build` | Build the `bootstrap` binary |
| `make test` | Run all Go tests (unit only, no DB required) |
| `make integration-test` | Run integration tests (requires `POSTGRES_DSN`) |
| `make e2e` | Run e2e tests (requires `LOCALSTACK_ENDPOINT` + `POSTGRES_DSN`) |
| `make localstack` | Start LocalStack Pro container |
| `make setup-localstack` | Provision all LocalStack resources |
| `make postgres` | Start plain Postgres container for integration tests |
| `make gosum` | Generate `go.sum` via the builder image (no local Go needed) |
| `make docker-build` | Build the container image |
| `make docker-push IMAGE_URI=...` | Tag and push to a registry |
| `make clean` | Remove the `bootstrap` binary |

## Podman notes (Fedora)

Set `DOCKER_CMD=podman` for all `make` targets that invoke a container runtime.
The start and setup scripts detect podman automatically and pass the required
flags (`--tls-verify=false`, `--format docker`, `--remove-signatures`) for ECR
image pushes.

The LocalStack container is started with `--privileged --user 0` so it can
access the rootless podman socket, which is mounted from
`/run/user/$(id -u)/podman/podman.sock`.

## Terraform (production)

The production Terraform lives in the `rosa-hyperfleet` repository on the
`team/kas-poc` branch:

```
terraform/
  modules/dynamo-status-bridge/   Reusable module
  config/dynamo-status-bridge-provisioning/   Per-region workspace
```

The module provisions:
- Lambda function (Image package type, VPC-attached)
- ECR repository
- CloudWatch log group (30-day retention)
- SQS dead-letter queue (14-day retention)
- IAM role with DynamoDB streams read, `rds-db:connect`, DLQ send, and VPC
  access policies
- One Event Source Mapping per entry in `status_stream_arns`
  (`for_each`, `ReportBatchItemFailures`, bisect on error, DLQ destination)

The `status_stream_arns` variable is a map of
`"{mc_name}-{tabletype}" → stream ARN` populated by the buildspec from each
MC's `kube-applier-dynamodb` Terraform outputs. S3 backend with per-region
state files.
