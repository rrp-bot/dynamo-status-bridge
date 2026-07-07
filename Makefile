BINARY     := bootstrap
CMD        := ./cmd/lambda
IMAGE_NAME := dynamo-status-bridge

.PHONY: build test integration-test e2e localstack postgres setup-localstack docker-build docker-push clean

build:
	CGO_ENABLED=0 GOOS=linux go build -o $(BINARY) $(CMD)

test:
	go test ./... -count=1

# integration-test: runs handler tests directly against a local Postgres.
# Requires: POSTGRES_DSN (use hack/start-postgres.sh for a local Postgres container).
integration-test:
	go test ./test/integration/... -v -count=1 -timeout 120s

# e2e: runs end-to-end tests against the Lambda running inside LocalStack Pro.
# RDS is provisioned inside LocalStack — no separate Postgres container needed.
# Run hack/start-localstack.sh + hack/setup-localstack.sh first.
# Requires: LOCALSTACK_ENDPOINT, POSTGRES_DSN (printed by setup-localstack.sh), MC_NAME (default: mc01)
e2e:
	go test ./test/e2e/... -v -count=1 -timeout 180s

localstack:
	./hack/start-localstack.sh

# postgres: starts a local Postgres container for integration tests only (not e2e).
# e2e tests use RDS inside LocalStack instead.
postgres:
	./hack/start-postgres.sh

# setup-localstack: creates RDS, ECR, builds + pushes image, creates DynamoDB tables,
# Lambda function, and event source mappings. Requires LocalStack Pro running.
# Requires: LOCALSTACK_ENDPOINT (default: http://localhost:4566)
setup-localstack:
	./hack/setup-localstack.sh

docker-build:
	docker build -t $(IMAGE_NAME):latest .

docker-push:
	@test -n "$(IMAGE_URI)" || (echo "IMAGE_URI is required, e.g. make docker-push IMAGE_URI=123456789.dkr.ecr.us-east-1.amazonaws.com/dynamo-status-bridge:abc123" && exit 1)
	docker tag $(IMAGE_NAME):latest $(IMAGE_URI)
	docker push $(IMAGE_URI)

clean:
	rm -f $(BINARY)
