FROM registry.access.redhat.com/ubi9/go-toolset:9.8-1782219569 AS builder

USER root
WORKDIR /app
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /bootstrap ./cmd/lambda

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8-1782191395
COPY --from=builder /bootstrap /bootstrap
USER 65532:65532
ENTRYPOINT ["/bootstrap"]
CMD [""]
