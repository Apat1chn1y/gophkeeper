package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nonexistent.json")
	c, err := LoadFrom(p)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if c.ServerAddress != "" || c.Token != "" {
		t.Fatalf("expected empty config, got %+v", c)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "config.json")
	c := &Config{ServerAddress: "http://x", Token: "t", UserID: "u", path: p}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadFrom(p)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.ServerAddress != "http://x" || got.Token != "t" || got.UserID != "u" {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestSaveUses0600Perms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	c := &Config{ServerAddress: "http://x", path: p}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", fi.Mode().Perm())
	}
}

func TestClearForgetsSession(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	c := &Config{ServerAddress: "http://x", Token: "t", UserID: "u", path: p}
	_ = c.Save()
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, _ := LoadFrom(p)
	if got.Token != "" || got.UserID != "" {
		t.Fatalf("session not cleared: %+v", got)
	}
	if got.ServerAddress != "http://x" {
		t.Fatalf("server address should survive logout")
	}
}

func TestLoadMalformedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	_ = os.WriteFile(p, []byte("{not json"), 0o600)
	if _, err := LoadFrom(p); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestPathEnvOverride(t *testing.T) {
	t.Setenv("GOPHKEEPER_CLIENT_CONFIG", "/tmp/custom.json")
	p, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if p != "/tmp/custom.json" {
		t.Fatalf("p = %q", p)
	}
}

func TestLoadFromDefaultPathMissing(t *testing.T) {
	t.Setenv("GOPHKEEPER_CLIENT_CONFIG", filepath.Join(t.TempDir(), "nope.json"))
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ServerAddress != "" {
		t.Fatalf("expected empty: %+v", c)
	}
}

func TestSaveFallsBackToDefaultPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auto.json")
	t.Setenv("GOPHKEEPER_CLIENT_CONFIG", p)
	c := &Config{ServerAddress: "http://auto"} // path пустой → Save сам его вычислит
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestLoadMalformedThenSave(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	_ = os.WriteFile(p, []byte("{bad"), 0o600)
	if _, err := LoadFrom(p); err == nil {
		t.Fatal("expected parse error")
	}
}
