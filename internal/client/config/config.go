// Package config manages the GophKeeper client's on-disk configuration
// file. The file is a small JSON document stored by default under
// ~/.gophkeeper/config.json.
//
// The path may be overridden with the GOPHKEEPER_CLIENT_CONFIG
// environment variable, which is primarily useful for tests.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds the persistent client state: the server address, the
// currently issued JWT and the user ID it belongs to.
type Config struct {
	// ServerAddress is the base URL of the GophKeeper server, e.g.
	// "http://localhost:8080".
	ServerAddress string `json:"server_address"`
	// Token is the JWT issued at the last successful login. It is empty
	// before the first login and after logout.
	Token string `json:"token,omitempty"`
	// UserID is the identifier of the authenticated user. It is empty
	// before the first login and after logout.
	UserID string `json:"user_id,omitempty"`

	path string `json:"-"`
}

// Path returns the effective config file path, honouring the
// GOPHKEEPER_CLIENT_CONFIG override.
func Path() (string, error) {
	if p := os.Getenv("GOPHKEEPER_CLIENT_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home dir: %w", err)
	}
	return filepath.Join(home, ".gophkeeper", "config.json"), nil
}

// Load reads the config from the default path. If the file does not
// exist, an empty Config bound to the default path is returned; this
// lets a freshly-installed client register without any prior setup.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(p)
}

// LoadFrom reads the config from an explicit path.
func LoadFrom(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{path: path}, nil
		}
		return nil, fmt.Errorf("config: read: %w", err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	c.path = path
	return &c, nil
}

// Save atomically writes the config to disk, creating the parent
// directory if necessary.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		c.path = p
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("config: write tmp: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("config: rename: %w", err)
	}
	return nil
}

// Clear forgets the session (token and user ID) but keeps the server
// address, then saves. Used by the "logout" command.
func (c *Config) Clear() error {
	c.Token = ""
	c.UserID = ""
	return c.Save()
}
