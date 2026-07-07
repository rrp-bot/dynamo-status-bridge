package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/psav/dynamo-status-bridge/internal/config"
	"github.com/psav/dynamo-status-bridge/internal/db"
	"github.com/psav/dynamo-status-bridge/internal/handler"
)

var h *handler.Handler

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Use a background context for the cold-start connection setup.
	// Individual invocations get their own deadline-scoped context.
	ctx := context.Background()

	client, err := db.New(ctx, cfg)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}

	h = handler.New(db.NewWriter(client.Pool()))
	slog.Info("dynamo-status-bridge initialised")
}

func main() {
	lambda.Start(h.Handle)
}
