// Package config handles on-disk persistence of the long-lived GitHub OAuth
// token used to authenticate against Copilot.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// EnvGitHubToken lets callers supply a GitHub OAuth token directly, bypassing
// the on-disk config (useful for headless or CI environments).
const EnvGitHubToken = "COPILOT_PROXY_GITHUB_TOKEN"

// Config is the persisted proxy configuration.
type Config struct {
	GitHubToken string `json:"github_token"`
}

// Dir returns the directory where the proxy stores its configuration.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "copilot-proxy"), nil
}

// Path returns the full path to the config file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the persisted config. A missing file is not an error; it returns
// an empty Config. An environment token, if set, takes precedence.
func Load() (*Config, error) {
	if tok := os.Getenv(EnvGitHubToken); tok != "" {
		return &Config{GitHubToken: tok}, nil
	}

	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config to disk with owner-only permissions.
func Save(c *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Clear removes the persisted config file, if present.
func Clear() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
