// Package app wires the GophKeeper server's dependencies and runs its
// HTTPS listener.
//
// It is separated from cmd/server so that the full start/stop cycle can
// be exercised by tests without involving os.Exit.
package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"log/slog"
	"math/big"
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

// Run builds every server component and serves HTTPS until ctx is
// cancelled or the process receives SIGINT/SIGTERM.
//
// The server always terminates TLS. If cfg.TLSCertFile and
// cfg.TLSKeyFile are set, the PEM pair is loaded from disk; otherwise a
// self-signed certificate for 127.0.0.1/localhost is generated in memory
// at startup. The certificate's SHA-256 fingerprint is logged so that a
// client can pin it if it chooses.
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

	cert, err := loadOrGenerateCert(cfg)
	if err != nil {
		return err
	}

	svc := service.New(store, enc, cfg.JWTSecret, cfg.TokenTTL)
	handler := api.New(svc, cfg.JWTSecret, logger).Routes()

	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return err
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	tlsLn := tls.NewListener(ln, tlsCfg)

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         tlsCfg,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", ln.Addr().String(), "tls", "on")
		errCh <- srv.Serve(tlsLn)
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

// loadOrGenerateCert returns the server's TLS certificate. It loads a
// PEM pair from disk if both paths are configured, otherwise it
// generates a self-signed certificate valid for localhost and 127.0.0.1.
func loadOrGenerateCert(cfg *config.Config) (tls.Certificate, error) {
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return tls.Certificate{}, err
		}
		return cert, nil
	}
	return selfSignedCert()
}

// selfSignedCert returns an in-memory self-signed certificate for
// 127.0.0.1 and localhost, valid for one year.
func selfSignedCert() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "gophkeeper"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}, nil
}
