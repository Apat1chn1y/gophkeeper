package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"gophkeeper/internal/server/config"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func baseConfig() *config.Config {
	return &config.Config{
		Host:          "127.0.0.1",
		Port:          0, // let the OS pick a free port
		StorageDSN:    ":memory:",
		JWTSecret:     []byte("0123456789abcdef0123456789abcdef"),
		EncryptionKey: []byte("0123456789abcdef0123456789abcdef"),
		TokenTTL:      time.Hour,
	}
}

func TestRunStartsAndStops(t *testing.T) {
	cfg := baseConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := Run(ctx, cfg, quietLogger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunRejectsBadEncryptionKey(t *testing.T) {
	cfg := baseConfig()
	cfg.EncryptionKey = []byte("tooshort")

	err := Run(context.Background(), cfg, quietLogger())
	if err == nil {
		t.Fatal("expected error for wrong encryption key size")
	}
}

func TestRunRejectsBadStorageDSN(t *testing.T) {
	cfg := baseConfig()
	cfg.StorageDSN = "/nonexistent-dir-x/gophkeeper.db"

	err := Run(context.Background(), cfg, quietLogger())
	if err == nil {
		t.Fatal("expected error for unwritable DSN")
	}
}

func TestRunRejectsBadListenAddr(t *testing.T) {
	cfg := baseConfig()
	cfg.Host = "999.999.999.999"
	cfg.Port = 1

	err := Run(context.Background(), cfg, quietLogger())
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}
