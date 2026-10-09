package config

import (
	"testing"
	"time"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestFromEnvDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"GOPHKEEPER_JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_ENCRYPTION_KEY": "0123456789abcdef0123456789abcdef",
	})
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 8080 {
		t.Fatalf("unexpected host/port: %s:%d", cfg.Host, cfg.Port)
	}
	if cfg.StorageDSN != "gophkeeper.db" {
		t.Fatalf("unexpected DSN: %s", cfg.StorageDSN)
	}
	if cfg.TokenTTL != 24*time.Hour {
		t.Fatalf("unexpected TTL: %v", cfg.TokenTTL)
	}
}

func TestFromEnvRejectsShortSecret(t *testing.T) {
	setEnv(t, map[string]string{
		"GOPHKEEPER_JWT_SECRET":     "short",
		"GOPHKEEPER_ENCRYPTION_KEY": "0123456789abcdef0123456789abcdef",
	})
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error for short JWT secret")
	}
}

func TestFromEnvRejectsWrongKeySize(t *testing.T) {
	setEnv(t, map[string]string{
		"GOPHKEEPER_JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_ENCRYPTION_KEY": "tooshort",
	})
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error for wrong encryption key size")
	}
}

func TestFromEnvRejectsBadPort(t *testing.T) {
	setEnv(t, map[string]string{
		"GOPHKEEPER_JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_ENCRYPTION_KEY": "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_PORT":           "not-a-port",
	})
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestAddr(t *testing.T) {
	c := &Config{Host: "0.0.0.0", Port: 9090}
	if c.Addr() != "0.0.0.0:9090" {
		t.Fatalf("Addr() = %q", c.Addr())
	}
}
