package config

import (
	"fmt"
	"os"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	// RDS connection
	RDSHost   string
	RDSPort   string
	RDSDBName string
	RDSUser   string

	// AWS region (used for IAM auth token generation)
	AWSRegion string

	// UseIAMAuth controls whether to generate an IAM auth token or use a plain DSN.
	// Set USE_IAM_AUTH=false for local/integration testing.
	UseIAMAuth bool

	// PlainDSN is used when UseIAMAuth=false (integration tests).
	PlainDSN string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	useIAM := os.Getenv("USE_IAM_AUTH") != "false"

	if !useIAM {
		dsn := os.Getenv("POSTGRES_DSN")
		if dsn == "" {
			return nil, fmt.Errorf("USE_IAM_AUTH=false requires POSTGRES_DSN to be set")
		}
		return &Config{
			UseIAMAuth: false,
			PlainDSN:   dsn,
		}, nil
	}

	cfg := &Config{
		UseIAMAuth: true,
		RDSHost:    os.Getenv("RDS_HOST"),
		RDSPort:    getEnvOrDefault("RDS_PORT", "5432"),
		RDSDBName:  getEnvOrDefault("RDS_DB", "statusbridge"),
		RDSUser:    getEnvOrDefault("RDS_USER", "statusbridge"),
		AWSRegion:  os.Getenv("AWS_REGION"),
	}

	if cfg.RDSHost == "" {
		return nil, fmt.Errorf("RDS_HOST is required")
	}
	if cfg.AWSRegion == "" {
		return nil, fmt.Errorf("AWS_REGION is required")
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
