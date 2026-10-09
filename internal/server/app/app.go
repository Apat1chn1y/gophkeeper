// Package app wires the GophKeeper server's dependencies and runs its
// HTTP listener.
//
// It is separated from cmd/server so that the full start/stop cycle can
// be exercised by tests without involving os.Exit.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gophkeeper/internal/crypto"
	"gophkeeper/internal/server/api"
	"gophkeeper/internal/server/config"
	"gophkeeper/internal/server/service"
	"gophkeeper/internal/server/storage/sqlite"
)

// shutdownTimeout bounds how long in-flight requests are allowed to
// complete during a graceful shutdown.
const shutdownTimeout = 10 * time.Second

// Run builds every server component and serves HTTP until ctx is
// cancelled or the process receives SIGINT/SIGTERM.
//
// It returns nil on a clean shutdown and a non-nil error on any other
// failure. If logger is nil, slog.Default() is used.
func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	store, err := sqlite.Open(cfg.StorageDSN)
	if err != nil {
		return err
	}
	defer store.Close()

	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return err
	}

	svc := service.New(store, enc, cfg.JWTSecret, cfg.TokenTTL)
	handler := api.New(svc, cfg.JWTSecret, logger).Routes()

	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Tie the server lifetime to both ctx and OS signals.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
