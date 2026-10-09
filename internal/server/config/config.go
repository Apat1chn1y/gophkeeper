// Package config loads the GophKeeper server configuration from
// environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gophkeeper/internal/crypto"
)

// Config holds all runtime parameters of the GophKeeper server.
type Config struct {
	// Host is the interface the HTTP server binds to.
	Host string
	// Port is the TCP port the HTTP server listens on.
	Port int
	// StorageDSN is the data source name passed to the storage backend.
	// For SQLite this is a file path; ":memory:" is supported for tests.
	StorageDSN string
	// JWTSecret is the HMAC key used to sign session tokens.
	JWTSecret []byte
	// EncryptionKey is the 32-byte AES key used to encrypt item payloads
	// at rest. It must be exactly crypto.KeySize bytes long.
	EncryptionKey []byte
	// TokenTTL is how long an issued JWT remains valid.
	TokenTTL time.Duration
}

// Addr returns the "host:port" string suitable for http.Server.
func (c *Config) Addr() string {
	return c.Host + ":" + strconv.Itoa(c.Port)
}

// FromEnv builds a Config from the process environment.
//
// Required variables:
//
//	GOPHKEEPER_JWT_SECRET       - at least 32 bytes
//	GOPHKEEPER_ENCRYPTION_KEY   - exactly 32 bytes
//
// Optional variables and their defaults:
//
//	GOPHKEEPER_HOST             127.0.0.1
//	GOPHKEEPER_PORT             8080
//	GOPHKEEPER_STORAGE_DSN      gophkeeper.db
//	GOPHKEEPER_TOKEN_TTL        24h
func FromEnv() (*Config, error) {
	cfg := &Config{
		Host:       getenv("GOPHKEEPER_HOST", "127.0.0.1"),
		StorageDSN: getenv("GOPHKEEPER_STORAGE_DSN", "gophkeeper.db"),
	}

	portStr := getenv("GOPHKEEPER_PORT", "8080")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("config: invalid GOPHKEEPER_PORT %q", portStr)
	}
	cfg.Port = port

	secret := os.Getenv("GOPHKEEPER_JWT_SECRET")
	if len(secret) < 32 {
		return nil, fmt.Errorf("config: GOPHKEEPER_JWT_SECRET must be at least 32 bytes")
	}
	cfg.JWTSecret = []byte(secret)

	key := os.Getenv("GOPHKEEPER_ENCRYPTION_KEY")
	if len(key) != crypto.KeySize {
		return nil, fmt.Errorf("config: GOPHKEEPER_ENCRYPTION_KEY must be exactly %d bytes", crypto.KeySize)
	}
	cfg.EncryptionKey = []byte(key)

	ttlStr := getenv("GOPHKEEPER_TOKEN_TTL", "24h")
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil || ttl <= 0 {
		return nil, fmt.Errorf("config: invalid GOPHKEEPER_TOKEN_TTL %q", ttlStr)
	}
	cfg.TokenTTL = ttl

	return cfg, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
