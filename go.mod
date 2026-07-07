module github.com/psav/dynamo-status-bridge

go 1.26.3

require (
	github.com/aws/aws-lambda-go v1.47.0
	github.com/aws/aws-sdk-go-v2 v1.41.5
	github.com/aws/aws-sdk-go-v2/config v1.32.13
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.15.25
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.43.1
	github.com/aws/aws-sdk-go-v2/service/rds v1.100.1
	github.com/jackc/pgx/v5 v5.7.5
	k8s.io/apimachinery v0.36.1
)
