package app

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/config"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

func setupTestEnvironmentWithKeys(t *testing.T) (string, func()) {
	root := t.TempDir()
	cleanup := func() {}

	// Creating necessary directories
	os.MkdirAll(root+"/config/keys", 0750)

	// Writing embedded contents to files
	assert.NilError(t, os.WriteFile(root+"/config/test.yaml", testConfig, 0640))
	assert.NilError(t, os.WriteFile(root+"/config/keys/test_swarm.key", testSwarmKey, 0640))

	// Generating and writing domain verification keys
	privKey, pubKey, err := generateDVKeys(nil, nil)
	if err != nil {
		t.Fatalf("Failed to generate domain verification keys: %v", err)
	}
	assert.NilError(t, os.WriteFile(root+"/config/keys/test.key", privKey, 0640))
	assert.NilError(t, os.WriteFile(root+"/config/keys/test.pub", pubKey, 0640))

	return root, cleanup
}

func TestExportConfigWithEmbeddedFilesAndGeneratedKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	root, cleanup := setupTestEnvironmentWithKeys(t)
	defer cleanup()

	app := newApp() // Assuming newApp initializes *cli.App with your commands

	args := []string{"tau", "--root", root, "config", "export", "--shape", "test"}
	err := app.RunContext(ctx, args)
	assert.NilError(t, err)
}

func TestExportConfigWithEmbeddedFilesAndGeneratedKeysToFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	root, cleanup := setupTestEnvironmentWithKeys(t)
	defer cleanup()

	outputFile := root + "/configExported.yaml"
	app := newApp() // Assuming newApp initializes *cli.App with your commands

	args := []string{"tau", "--root", root, "config", "export", "--shape", "test", outputFile}
	err := app.RunContext(ctx, args)
	assert.NilError(t, err)

	// Read the output file
	exportedData, err := os.ReadFile(outputFile)
	assert.NilError(t, err)

	// Example assertion - check if the file contains expected strings
	assert.Assert(t, containsString(exportedData, "shape: test"), "Exported config should contain the shape")
	assert.Assert(t, containsString(exportedData, "swarmkey"), "Exported config should contain the swarmkey")
}

// containsString is a helper function to check if the byte slice contains the specified string
func containsString(data []byte, str string) bool {
	return strings.Contains(string(data), str)
}

func TestExportConfigEncryptionOfFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	root, cleanup := setupTestEnvironmentWithKeys(t)
	defer cleanup()

	testPassword = "secret"
	outputFile := root + "/configProtectedExport.yaml"

	// Setup your app and context similarly to your actual app's initialization
	app := newApp() // newApp should setup CLI app with the exportConfig command

	// Running the exportConfig command with encryption enabled
	args := []string{"tau", "--root", root, "config", "export", "--shape", "test", "--protect", outputFile}
	err := app.RunContext(ctx, args)
	assert.NilError(t, err)

	// Reading the output file
	exportedData, err := os.ReadFile(outputFile)
	assert.NilError(t, err)

	// Parsing the YAML to access encrypted fields
	var exportedBundle config.Bundle
	err = yaml.Unmarshal(exportedData, &exportedBundle)
	assert.NilError(t, err)

	swarmKey, err := decryptAndBase64Decode(exportedBundle.Swarmkey, testPassword)
	assert.NilError(t, err)

	// Compare decryptedSwarmKey with the original swarm key content used in setupTestEnvironmentWithKeys
	assert.DeepEqual(t, testSwarmKey, swarmKey)
}

// Helper function to base64 decode then decrypt
func decryptAndBase64Decode(encodedEncryptedData string, password string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encodedEncryptedData)
	if err != nil {
		return nil, err
	}
	return decrypt(data, password) // Use your actual decryption function
}
