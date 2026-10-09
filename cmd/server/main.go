// Command server runs the GophKeeper HTTP server.
package main

import (
	"context"
	"log/slog"
	"os"

	"gophkeeper/internal/server/app"
	"gophkeeper/internal/server/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr,
		&slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}
	if err := app.Run(context.Background(), cfg, logger); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}
