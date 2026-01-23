package keypair

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	priv := New()
	assert.NotNil(t, priv, "New should return a non-nil private key")

	// Verify it's an Ed25519 key
	assert.Equal(t, priv.Type().String(), "Ed25519")
}

func TestNewRaw(t *testing.T) {
	raw := NewRaw()
	assert.NotNil(t, raw, "NewRaw should return non-nil data")
	assert.True(t, len(raw) > 0, "NewRaw should return non-empty data")
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test.key")

	// Generate a new key
	priv := New()
	require.NotNil(t, priv)

	// Save the key
	err := Save(priv, keyPath)
	require.NoError(t, err, "Save should not return an error")

	// Check file exists
	_, err = os.Stat(keyPath)
	require.NoError(t, err, "Key file should exist")

	// Load the key
	loaded, err := Load(keyPath)
	require.NoError(t, err, "Load should not return an error")
	require.NotNil(t, loaded, "Loaded key should not be nil")

	// Verify keys are equal
	assert.True(t, priv.Equals(loaded), "Loaded key should equal original key")
}

func TestLoad_NonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/to/key")
	assert.Error(t, err, "Load should return an error for non-existent file")
}

func TestLoadRaw(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test.key")

	// Generate and save a key
	priv := New()
	require.NotNil(t, priv)
	err := Save(priv, keyPath)
	require.NoError(t, err)

	// Load raw
	raw, err := LoadRaw(keyPath)
	require.NoError(t, err, "LoadRaw should not return an error")
	assert.True(t, len(raw) > 0, "LoadRaw should return non-empty data")
}

func TestLoadRaw_NonexistentFile(t *testing.T) {
	_, err := LoadRaw("/nonexistent/path/to/key")
	assert.Error(t, err, "LoadRaw should return an error for non-existent file")
}

func TestNewPersistant_NewKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "persistent.key")

	// Create new persistent key
	raw, err := NewPersistant(keyPath)
	require.NoError(t, err, "NewPersistant should not return an error")
	assert.True(t, len(raw) > 0, "NewPersistant should return non-empty data")

	// Verify file was created
	_, err = os.Stat(keyPath)
	require.NoError(t, err, "Key file should exist")
}

func TestNewPersistant_ExistingKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "persistent.key")

	// Create initial key
	raw1, err := NewPersistant(keyPath)
	require.NoError(t, err)

	// Load again - should return same key
	raw2, err := NewPersistant(keyPath)
	require.NoError(t, err)

	assert.Equal(t, raw1, raw2, "NewPersistant should return same key for existing file")
}

func TestLoadRawFromEnv(t *testing.T) {
	// Generate a key and encode it
	priv := New()
	require.NotNil(t, priv)

	raw := NewRaw()
	encoded := base64.StdEncoding.EncodeToString(raw)

	// Set environment variable
	t.Setenv("TAUBYTE_KEY", encoded)

	// Load from env
	loaded := LoadRawFromEnv()
	assert.NotNil(t, loaded, "LoadRawFromEnv should return non-nil data")
	assert.Equal(t, raw, loaded, "LoadRawFromEnv should return the correct key data")
}

func TestLoadRawFromEnv_NotSet(t *testing.T) {
	// Ensure env variable is not set
	os.Unsetenv("TAUBYTE_KEY")

	loaded := LoadRawFromEnv()
	assert.Nil(t, loaded, "LoadRawFromEnv should return nil when env var is not set")
}

func TestLoadRawFromEnv_InvalidBase64(t *testing.T) {
	t.Setenv("TAUBYTE_KEY", "not-valid-base64!!!")

	loaded := LoadRawFromEnv()
	assert.Nil(t, loaded, "LoadRawFromEnv should return nil for invalid base64")
}

func TestLoadRawFromString(t *testing.T) {
	raw := NewRaw()
	encoded := base64.StdEncoding.EncodeToString(raw)

	loaded := LoadRawFromString(encoded)
	assert.NotNil(t, loaded, "LoadRawFromString should return non-nil data")
	assert.Equal(t, raw, loaded, "LoadRawFromString should return the correct key data")
}

func TestLoadRawFromString_InvalidBase64(t *testing.T) {
	loaded := LoadRawFromString("not-valid-base64!!!")
	assert.Nil(t, loaded, "LoadRawFromString should return nil for invalid base64")
}

func TestLoadRawFromString_Empty(t *testing.T) {
	loaded := LoadRawFromString("")
	// Empty string is valid base64 that decodes to empty slice
	assert.NotNil(t, loaded, "LoadRawFromString should handle empty string")
	assert.Equal(t, 0, len(loaded), "LoadRawFromString of empty string should return empty slice")
}
