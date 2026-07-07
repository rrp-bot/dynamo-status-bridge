// Package e2e contains end-to-end tests that run the Lambda as a real container
// image inside LocalStack. The tests write items directly to DynamoDB (mimicking
// kube-applier-aws), then poll RDS until the Lambda has processed the stream
// records and written the rows.
//
// Prerequisites (see hack/setup-localstack.sh):
//   - LocalStack Pro running with lambda, ecr, dynamodb, dynamodbstreams
//   - Lambda image built and pushed to local ECR
//   - DynamoDB tables created with streams enabled
//   - Lambda function created and ESMs wired up
//   - Postgres running and accessible
//
// Required environment variables:
//   - LOCALSTACK_ENDPOINT  e.g. http://localhost:4566
//   - POSTGRES_DSN         DSN for the local Postgres (host-side port)
//   - MC_NAME              management cluster name prefix (default: mc01)
package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/jackc/pgx/v5/pgxpool"

	bridgeconfig "github.com/psav/dynamo-status-bridge/internal/config"
	"github.com/psav/dynamo-status-bridge/internal/db"
)

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("LOCALSTACK_ENDPOINT") == "" {
		t.Skip("skipping e2e test: LOCALSTACK_ENDPOINT not set")
	}
	if os.Getenv("POSTGRES_DSN") == "" {
		t.Skip("skipping e2e test: POSTGRES_DSN not set")
	}
}

func mcName() string {
	if mc := os.Getenv("MC_NAME"); mc != "" {
		return mc
	}
	return "mc01"
}

// setupClients creates a DynamoDB client pointing at LocalStack and a pgxpool
// pointing at the local Postgres.
func setupClients(t *testing.T) (*dynamodb.Client, *pgxpool.Pool) {
	t.Helper()

	endpoint := os.Getenv("LOCALSTACK_ENDPOINT")

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		awsconfig.WithBaseEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("loading AWS config: %v", err)
	}
	ddbClient := dynamodb.NewFromConfig(awsCfg)

	cfg := &bridgeconfig.Config{
		UseIAMAuth: false,
		PlainDSN:   os.Getenv("POSTGRES_DSN"),
	}
	client, err := db.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connecting to postgres: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return ddbClient, client.Pool()
}

// putItem writes a DynamoDB item mimicking what kube-applier-aws writes.
func putItem(ctx context.Context, t *testing.T, ddb *dynamodb.Client, tableName, docID, mc, clusterID string, extra map[string]dbtypes.AttributeValue) {
	t.Helper()

	item := map[string]dbtypes.AttributeValue{
		"documentID": &dbtypes.AttributeValueMemberS{Value: docID},
		"version":    &dbtypes.AttributeValueMemberN{Value: "1"},
		"updateTime": &dbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
		"spec": &dbtypes.AttributeValueMemberM{Value: map[string]dbtypes.AttributeValue{
			"managementCluster": &dbtypes.AttributeValueMemberS{Value: mc},
			"clusterID":         &dbtypes.AttributeValueMemberS{Value: clusterID},
			"targetItem": &dbtypes.AttributeValueMemberM{Value: map[string]dbtypes.AttributeValue{
				"group":    &dbtypes.AttributeValueMemberS{Value: "apps"},
				"version":  &dbtypes.AttributeValueMemberS{Value: "v1"},
				"resource": &dbtypes.AttributeValueMemberS{Value: "deployments"},
				"name":     &dbtypes.AttributeValueMemberS{Value: "my-deploy"},
			}},
		}},
		"status": &dbtypes.AttributeValueMemberM{Value: map[string]dbtypes.AttributeValue{
			"conditions": &dbtypes.AttributeValueMemberL{Value: []dbtypes.AttributeValue{
				&dbtypes.AttributeValueMemberM{Value: map[string]dbtypes.AttributeValue{
					"type":               &dbtypes.AttributeValueMemberS{Value: "Successful"},
					"status":             &dbtypes.AttributeValueMemberS{Value: "True"},
					"reason":             &dbtypes.AttributeValueMemberS{Value: "NoErrors"},
					"message":            &dbtypes.AttributeValueMemberS{Value: ""},
					"lastTransitionTime": &dbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
				}},
			}},
		}},
	}

	for k, v := range extra {
		item[k] = v
	}

	_, err := ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})
	if err != nil {
		t.Fatalf("PutItem to %s: %v", tableName, err)
	}
}

// deleteItem removes a DynamoDB item, triggering a REMOVE stream event.
func deleteItem(ctx context.Context, t *testing.T, ddb *dynamodb.Client, tableName, docID string) {
	t.Helper()
	_, err := ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]dbtypes.AttributeValue{
			"documentID": &dbtypes.AttributeValueMemberS{Value: docID},
		},
	})
	if err != nil {
		t.Fatalf("DeleteItem from %s: %v", tableName, err)
	}
}

// pollUntil retries f every interval until it returns true or timeout expires.
func pollUntil(t *testing.T, timeout, interval time.Duration, f func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

// queryExists returns true if a row exists in tableName for the given doc+mc.
func queryExists(t *testing.T, pool *pgxpool.Pool, pgTable, docID, mc string) bool {
	t.Helper()
	var count int
	err := pool.QueryRow(context.Background(),
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE document_id=$1 AND management_cluster=$2", pgTable),
		docID, mc,
	).Scan(&count)
	if err != nil {
		t.Logf("queryExists error: %v", err)
		return false
	}
	return count > 0
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestE2E_ReadDesire_InsertAndDelete(t *testing.T) {
	requireE2E(t)
	ddb, pool := setupClients(t)
	ctx := context.Background()
	mc := mcName()

	tableName := fmt.Sprintf("%s-status-readdesires", mc)
	docID := fmt.Sprintf("e2e-read-%d", time.Now().UnixNano())

	t.Logf("Writing INSERT to DynamoDB table %s, documentID=%s", tableName, docID)
	putItem(ctx, t, ddb, tableName, docID, mc, "cluster-e2e",
		map[string]dbtypes.AttributeValue{
			"status_kubeContent": &dbtypes.AttributeValueMemberS{
				Value: `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"my-deploy"}}`,
			},
		},
	)

	t.Log("Waiting for Lambda to process the INSERT and write to Postgres...")
	ok := pollUntil(t, 60*time.Second, 2*time.Second, func() bool {
		return queryExists(t, pool, "read_desire_statuses", docID, mc)
	})
	if !ok {
		t.Fatal("timed out waiting for read_desire_statuses row to appear")
	}
	t.Log("Row found in read_desire_statuses")

	// Verify kube_content was written
	var kubeContent *string
	err := pool.QueryRow(context.Background(),
		"SELECT kube_content::text FROM read_desire_statuses WHERE document_id=$1 AND management_cluster=$2",
		docID, mc,
	).Scan(&kubeContent)
	if err != nil {
		t.Fatalf("querying kube_content: %v", err)
	}
	if kubeContent == nil {
		t.Error("expected kube_content to be set, got NULL")
	} else {
		t.Logf("kube_content: %s", *kubeContent)
	}

	// Trigger REMOVE
	t.Log("Deleting item from DynamoDB (triggers REMOVE stream event)...")
	deleteItem(ctx, t, ddb, tableName, docID)

	t.Log("Waiting for Lambda to process the REMOVE and delete from Postgres...")
	ok = pollUntil(t, 60*time.Second, 2*time.Second, func() bool {
		return !queryExists(t, pool, "read_desire_statuses", docID, mc)
	})
	if !ok {
		t.Fatal("timed out waiting for read_desire_statuses row to be deleted")
	}
	t.Log("Row deleted from read_desire_statuses")
}

func TestE2E_ApplyDesire_InsertAndDelete(t *testing.T) {
	requireE2E(t)
	ddb, pool := setupClients(t)
	ctx := context.Background()
	mc := mcName()

	tableName := fmt.Sprintf("%s-status-applydesires", mc)
	docID := fmt.Sprintf("e2e-apply-%d", time.Now().UnixNano())

	t.Logf("Writing INSERT to DynamoDB table %s, documentID=%s", tableName, docID)
	putItem(ctx, t, ddb, tableName, docID, mc, "cluster-e2e",
		map[string]dbtypes.AttributeValue{},
	)

	t.Log("Waiting for Lambda to write to apply_desire_statuses...")
	ok := pollUntil(t, 60*time.Second, 2*time.Second, func() bool {
		return queryExists(t, pool, "apply_desire_statuses", docID, mc)
	})
	if !ok {
		t.Fatal("timed out waiting for apply_desire_statuses row")
	}
	t.Log("Row found in apply_desire_statuses")

	deleteItem(ctx, t, ddb, tableName, docID)

	ok = pollUntil(t, 60*time.Second, 2*time.Second, func() bool {
		return !queryExists(t, pool, "apply_desire_statuses", docID, mc)
	})
	if !ok {
		t.Fatal("timed out waiting for apply_desire_statuses row to be deleted")
	}
	t.Log("Row deleted from apply_desire_statuses")
}

func TestE2E_DeleteDesire_InsertAndDelete(t *testing.T) {
	requireE2E(t)
	ddb, pool := setupClients(t)
	ctx := context.Background()
	mc := mcName()

	tableName := fmt.Sprintf("%s-status-deletedesires", mc)
	docID := fmt.Sprintf("e2e-delete-%d", time.Now().UnixNano())

	t.Logf("Writing INSERT to DynamoDB table %s, documentID=%s", tableName, docID)
	putItem(ctx, t, ddb, tableName, docID, mc, "cluster-e2e",
		map[string]dbtypes.AttributeValue{},
	)

	t.Log("Waiting for Lambda to write to delete_desire_statuses...")
	ok := pollUntil(t, 60*time.Second, 2*time.Second, func() bool {
		return queryExists(t, pool, "delete_desire_statuses", docID, mc)
	})
	if !ok {
		t.Fatal("timed out waiting for delete_desire_statuses row")
	}
	t.Log("Row found in delete_desire_statuses")

	deleteItem(ctx, t, ddb, tableName, docID)

	ok = pollUntil(t, 60*time.Second, 2*time.Second, func() bool {
		return !queryExists(t, pool, "delete_desire_statuses", docID, mc)
	})
	if !ok {
		t.Fatal("timed out waiting for delete_desire_statuses row to be deleted")
	}
	t.Log("Row deleted from delete_desire_statuses")
}
