// Package db manages the PostgreSQL connection and schema migration.
package db

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rds/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	bridgeconfig "github.com/psav/dynamo-status-bridge/internal/config"
)

//go:embed schema/001_initial.sql
var initialSchema string

// Client wraps a pgxpool and exposes schema migration.
type Client struct {
	pool *pgxpool.Pool
}

// New creates a new Client and applies the schema.
// Uses IAM auth token generation when cfg.UseIAMAuth is true,
// or a plain DSN when false (integration tests).
func New(ctx context.Context, cfg *bridgeconfig.Config) (*Client, error) {
	dsn, err := buildDSN(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("building DSN: %w", err)
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing DSN: %w", err)
	}

	// When using IAM auth the token expires every 15 minutes.
	// BeforeConnect is called for every new physical connection, regenerating
	// the token so we never use a stale one.
	if cfg.UseIAMAuth {
		poolCfg.BeforeConnect = func(ctx context.Context, connCfg *pgx.ConnConfig) error {
			token, err := generateIAMToken(ctx, cfg)
			if err != nil {
				return fmt.Errorf("generating IAM auth token: %w", err)
			}
			connCfg.Password = token
			return nil
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating pgx pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	client := &Client{pool: pool}
	if err := client.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("running schema migration: %w", err)
	}

	slog.Info("postgres connection established")
	return client, nil
}

// Close releases all pool connections.
func (c *Client) Close() {
	c.pool.Close()
}

// Pool returns the underlying pgxpool for use by the Writer.
func (c *Client) Pool() *pgxpool.Pool {
	return c.pool
}

// migrate applies the embedded SQL schema idempotently (CREATE TABLE IF NOT EXISTS).
func (c *Client) migrate(ctx context.Context) error {
	_, err := c.pool.Exec(ctx, initialSchema)
	if err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}
	slog.Info("schema migration applied")
	return nil
}

// buildDSN returns the connection string appropriate for the config.
func buildDSN(ctx context.Context, cfg *bridgeconfig.Config) (string, error) {
	if !cfg.UseIAMAuth {
		return cfg.PlainDSN, nil
	}

	token, err := generateIAMToken(ctx, cfg)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"host=%s port=%s dbname=%s user=%s password=%s sslmode=require",
		cfg.RDSHost, cfg.RDSPort, cfg.RDSDBName, cfg.RDSUser, token,
	), nil
}

// generateIAMToken uses the AWS RDS auth signer to produce a short-lived
// authentication token from the Lambda execution role credentials.
func generateIAMToken(ctx context.Context, cfg *bridgeconfig.Config) (string, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.AWSRegion))
	if err != nil {
		return "", fmt.Errorf("loading AWS config: %w", err)
	}

	endpoint := fmt.Sprintf("%s:%s", cfg.RDSHost, cfg.RDSPort)
	token, err := auth.BuildAuthToken(ctx, endpoint, cfg.AWSRegion, cfg.RDSUser, awsCfg.Credentials)
	if err != nil {
		return "", fmt.Errorf("building RDS IAM auth token: %w", err)
	}

	return token, nil
}
