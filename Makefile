BINARY     := bootstrap
CMD        := ./cmd/lambda
IMAGE_NAME := dynamo-status-bridge

.PHONY: build test integration-test localstack postgres docker-build docker-push

build:
	CGO_ENABLED=0 GOOS=linux go build -o $(BINARY) $(CMD)

test:
	go test ./... -count=1

integration-test:
	go test ./test/integration/... -v -count=1 -timeout 120s

localstack:
	./hack/start-localstack.sh

postgres:
	./hack/start-postgres.sh

docker-build:
	docker build -t $(IMAGE_NAME):latest .

docker-push:
	@test -n "$(IMAGE_URI)" || (echo "IMAGE_URI is required, e.g. make docker-push IMAGE_URI=123456789.dkr.ecr.us-east-1.amazonaws.com/dynamo-status-bridge:abc123" && exit 1)
	docker tag $(IMAGE_NAME):latest $(IMAGE_URI)
	docker push $(IMAGE_URI)

clean:
	rm -f $(BINARY)
