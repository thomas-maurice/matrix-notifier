package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

const validConfig = `
matrix:
  homeserver: "http://localhost:8008"
  user_id: "@bot:localhost"
  password: "pw"
admin_token_hash: "$argon2id$fake"
`

func TestLoadValidConfigAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, validConfig))
	require.NoError(t, err)
	// Defaults must let a minimal config run out of the box.
	assert.Equal(t, ":8686", cfg.Listen)
	assert.Equal(t, "sqlite", cfg.Database.Type)
	assert.Equal(t, "./data/notifier.db", cfg.Database.URI)
	assert.Equal(t, "info", cfg.LogLevel)
}

// Every required key missing must be named in the error — a half-configured
// bot must not start and must say why.
func TestLoadReportsAllMissingKeys(t *testing.T) {
	_, err := Load(writeConfig(t, `listen: ":1234"`))
	require.Error(t, err)
	for _, key := range []string{"matrix.homeserver", "matrix.user_id", "matrix.password", "admin_token_hash"} {
		assert.Contains(t, err.Error(), key)
	}
}

func TestLoadRejectsUnknownDatabaseType(t *testing.T) {
	_, err := Load(writeConfig(t, validConfig+"\ndatabase:\n  type: mongodb\n  uri: x\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mongodb")
}

// Secrets are usually injected via environment; the env override contract
// must not silently break.
func TestEnvOverride(t *testing.T) {
	t.Setenv("TOCSIN_MATRIX_PASSWORD", "from-env")
	cfg, err := Load(writeConfig(t, validConfig))
	require.NoError(t, err)
	assert.Equal(t, "from-env", cfg.Matrix.Password)
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	require.Error(t, err)
}
