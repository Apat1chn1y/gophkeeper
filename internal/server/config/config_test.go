package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// setEnv prepares the process environment for a Config test.
//
// It first clears every GOPHKEEPER_* variable inherited from the shell,
// so tests are hermetic regardless of the developer's environment. This
// matters because os.LookupEnv (used by getenv) distinguishes "unset"
// from "set to empty": if the shell exported GOPHKEEPER_STORAGE_DSN, the
// default-value assertions would otherwise fail.
//
// After clearing, the supplied key/value pairs are installed via
// t.Setenv, which restores the previous value (or unsets) on cleanup.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, e := range os.Environ() {
		k, _, ok := strings.Cut(e, "=")
		if !ok || !strings.HasPrefix(k, "GOPHKEEPER_") {
			continue
		}
		prev, had := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(k, prev)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
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

func TestFromEnvTLSRequiresPair(t *testing.T) {
	setEnv(t, map[string]string{
		"GOPHKEEPER_JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_ENCRYPTION_KEY": "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_TLS_CERT":       "/tmp/cert.pem",
		// GOPHKEEPER_TLS_KEY intentionally missing
	})
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error when only TLS_CERT is set")
	}
}

func TestFromEnvTLSPairAccepted(t *testing.T) {
	setEnv(t, map[string]string{
		"GOPHKEEPER_JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_ENCRYPTION_KEY": "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_TLS_CERT":       "/tmp/cert.pem",
		"GOPHKEEPER_TLS_KEY":        "/tmp/key.pem",
	})
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.TLSCertFile != "/tmp/cert.pem" || cfg.TLSKeyFile != "/tmp/key.pem" {
		t.Fatalf("unexpected TLS config: %+v", cfg)
	}
}

func TestFromEnvLookupEnvEmptyOverride(t *testing.T) {
	// An explicitly-empty value must be returned as-is, not replaced by
	// the default. This exercises the os.LookupEnv-based getenv.
	setEnv(t, map[string]string{
		"GOPHKEEPER_JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_ENCRYPTION_KEY": "0123456789abcdef0123456789abcdef",
		"GOPHKEEPER_HOST":           "",
	})
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Host != "" {
		t.Fatalf("Host = %q, want empty (explicit)", cfg.Host)
	}
}
