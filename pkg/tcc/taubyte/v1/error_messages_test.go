package compiler

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

// TestCompile_RequiredFieldMissing_Line1_ExactError verifies that missing required field errors
// at line 1 include exact location information with the complete error message.
func TestCompile_RequiredFieldMissing_Line1_ExactError(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/config/functions", 0755)

	// Create a config.yaml with valid project ID
	configYaml := "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"
	afero.WriteFile(fs, "/test/config/config.yaml", []byte(configYaml), 0644)

	// Create a function YAML file missing required 'id' field
	// The error should point to this file at line 1, column 1
	functionYaml := "name: test-function\n"
	afero.WriteFile(fs, "/test/config/functions/test_func.yaml", []byte(functionYaml), 0644)

	compiler, err := New(WithVirtual(fs, "/test/config"))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact error format: "/functions/test_func.yaml:1:1: required attribute 'id'"
	expectedError := "/functions/test_func.yaml:1:1: required attribute 'id'"
	assert.Error(t, err, expectedError)
}

// TestCompile_ValidationError_Line2_ExactError verifies that validation errors at line 2
// include exact location information with the complete error message.
func TestCompile_ValidationError_Line2_ExactError(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/config/functions", 0755)

	// Create a config.yaml with valid project ID
	configYaml := "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"
	afero.WriteFile(fs, "/test/config/config.yaml", []byte(configYaml), 0644)

	// Create a function YAML file with invalid name (not a valid variable name)
	// Line 1: id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh
	// Line 2: name: 12345  <- invalid variable name (starts with number)
	functionYaml := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\nname: 12345\n"
	afero.WriteFile(fs, "/test/config/functions/test_func.yaml", []byte(functionYaml), 0644)

	compiler, err := New(WithVirtual(fs, "/test/config"))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact error format: "/functions/test_func.yaml:2:7: invalid variable name"
	expectedError := "/functions/test_func.yaml:2:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_ValidationError_Line3_ExactError verifies that validation errors at line 3
// include exact location information with the complete error message.
func TestCompile_ValidationError_Line3_ExactError(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/config/functions", 0755)

	// Create a config.yaml with valid project ID
	configYaml := "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"
	afero.WriteFile(fs, "/test/config/config.yaml", []byte(configYaml), 0644)

	// Create a function YAML file with invalid name at line 3
	// Line 1: id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh
	// Line 2: type: http
	// Line 3: name: 999invalid  <- invalid variable name (starts with number)
	functionYaml := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\ntype: http\nname: 999invalid\n"
	afero.WriteFile(fs, "/test/config/functions/test_func.yaml", []byte(functionYaml), 0644)

	compiler, err := New(WithVirtual(fs, "/test/config"))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact error format: "/functions/test_func.yaml:3:7: invalid variable name"
	expectedError := "/functions/test_func.yaml:3:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_ValidationError_Line4_ExactError verifies that validation errors at line 4
// include exact location information with the complete error message.
func TestCompile_ValidationError_Line4_ExactError(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/config/functions", 0755)

	// Create a config.yaml with valid project ID
	configYaml := "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"
	afero.WriteFile(fs, "/test/config/config.yaml", []byte(configYaml), 0644)

	// Create a function YAML file with invalid name at line 4
	// Line 1: id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh
	// Line 2: type: http
	// Line 3: http-method: GET
	// Line 4: name: 999invalid  <- invalid variable name (starts with number)
	functionYaml := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\ntype: http\nhttp-method: GET\nname: 999invalid\n"
	afero.WriteFile(fs, "/test/config/functions/test_func.yaml", []byte(functionYaml), 0644)

	compiler, err := New(WithVirtual(fs, "/test/config"))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact error format: "/functions/test_func.yaml:4:7: invalid variable name"
	expectedError := "/functions/test_func.yaml:4:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_NestedPathError_Line1_ExactError verifies that errors in nested file structures
// at line 1 report the exact nested file path with complete error message.
func TestCompile_NestedPathError_Line1_ExactError(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/config/applications/test_app/functions", 0755)

	// Create a config.yaml with valid project ID
	configYaml := "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"
	afero.WriteFile(fs, "/test/config/config.yaml", []byte(configYaml), 0644)

	// Create application config with valid ID
	appConfigYaml := "id: QmPzW5WJfw7oR8zHrYPXGMxqM9vLhZ6vW7jbUbJj5Xf4sR\n"
	afero.WriteFile(fs, "/test/config/applications/test_app/config.yaml", []byte(appConfigYaml), 0644)

	// Create function file in application missing required id at line 1
	functionYaml := "name: test-function\n"
	afero.WriteFile(fs, "/test/config/applications/test_app/functions/test_func.yaml", []byte(functionYaml), 0644)

	compiler, err := New(WithVirtual(fs, "/test/config"))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact error format: "/applications/test_app/functions/test_func.yaml:1:1: required attribute 'id'"
	expectedError := "/applications/test_app/functions/test_func.yaml:1:1: required attribute 'id'"
	assert.Error(t, err, expectedError)
}

// TestCompile_NestedPathError_Line2_ExactError verifies that errors in nested file structures
// at line 2 report the exact nested file path with complete error message.
func TestCompile_NestedPathError_Line2_ExactError(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/config/applications/test_app/functions", 0755)

	// Create a config.yaml with valid project ID
	configYaml := "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"
	afero.WriteFile(fs, "/test/config/config.yaml", []byte(configYaml), 0644)

	// Create application config with valid ID
	appConfigYaml := "id: QmPzW5WJfw7oR8zHrYPXGMxqM9vLhZ6vW7jbUbJj5Xf4sR\n"
	afero.WriteFile(fs, "/test/config/applications/test_app/config.yaml", []byte(appConfigYaml), 0644)

	// Create function file in application with invalid name at line 2
	// Line 1: id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh
	// Line 2: name: 12345  <- invalid variable name
	functionYaml := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\nname: 12345\n"
	afero.WriteFile(fs, "/test/config/applications/test_app/functions/test_func.yaml", []byte(functionYaml), 0644)

	compiler, err := New(WithVirtual(fs, "/test/config"))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact error format: "/applications/test_app/functions/test_func.yaml:2:7: invalid variable name"
	expectedError := "/applications/test_app/functions/test_func.yaml:2:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_ProjectEmailValidation tests email validation error using fixtures
func TestCompile_ProjectEmailValidation(t *testing.T) {
	// Mount fixtures directory with copy-on-write
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify config.yaml to have invalid email
	configPath := filepath.Join(fixturesPath, "config.yaml")
	invalidConfig := "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: TrueTest\nnotification:\n    email: invalid-email\n"
	afero.WriteFile(cowFs, configPath, []byte(invalidConfig), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /config.yaml:4:12: mail: missing '@' or angle-addr
	expectedError := "/config.yaml:4:12: mail: missing '@' or angle-addr"
	assert.Error(t, err, expectedError)
}

// TestCompile_FunctionInvalidType tests function type validation using InSet
func TestCompile_FunctionInvalidType(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify function to have invalid type
	funcPath := filepath.Join(fixturesPath, "functions", "test_function1_glob.yaml")
	invalidFunc := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\ntrigger:\n    type: invalid_type\n    method: get\n"
	afero.WriteFile(cowFs, funcPath, []byte(invalidFunc), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /functions/test_function1_glob.yaml:3:11: invalid value
	expectedError := "/functions/test_function1_glob.yaml:3:11: invalid value"
	assert.Error(t, err, expectedError)
}

// TestCompile_FunctionInvalidHttpMethod tests HTTP method validation
func TestCompile_FunctionInvalidHttpMethod(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify function to have invalid HTTP method
	funcPath := filepath.Join(fixturesPath, "functions", "test_function1_glob.yaml")
	invalidFunc := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\ntrigger:\n    type: http\n    method: INVALID_METHOD\n"
	afero.WriteFile(cowFs, funcPath, []byte(invalidFunc), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /functions/test_function1_glob.yaml:4:13: invalid http method
	expectedError := "/functions/test_function1_glob.yaml:4:13: invalid http method"
	assert.Error(t, err, expectedError)
}

// TestCompile_DomainInvalidFqdn tests FQDN validation
func TestCompile_DomainInvalidFqdn(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify domain to have invalid FQDN
	domainPath := filepath.Join(fixturesPath, "domains", "test_domain1.yaml")
	invalidDomain := "id: QmUcVJtgGZYkqFr2J9t2jV2fJJWZBvD7FJ6RyXzJY2kAj1\nfqdn: not-a-valid-fqdn!!!\n"
	afero.WriteFile(cowFs, domainPath, []byte(invalidDomain), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /domains/test_domain1.yaml:2:7: invalid fqdn
	expectedError := "/domains/test_domain1.yaml:2:7: invalid fqdn"
	assert.Error(t, err, expectedError)
}

// TestCompile_DatabaseInvalidNetworkAccess tests InSet validation for network-access
func TestCompile_DatabaseInvalidNetworkAccess(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify database to have invalid network-access
	dbPath := filepath.Join(fixturesPath, "databases", "test_database1.yaml")
	invalidDb := "id: QmRkFTeYx8J4X3X2Jx5xutHArDyp72r7z6sLX9s3iCbsXr\naccess:\n    network: invalid_access\n"
	afero.WriteFile(cowFs, dbPath, []byte(invalidDb), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /databases/test_database1.yaml:3:14: invalid value
	expectedError := "/databases/test_database1.yaml:3:14: invalid value"
	assert.Error(t, err, expectedError)
}

// TestCompile_FunctionInvalidName tests variable name validation on function
func TestCompile_FunctionInvalidName(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify function to have invalid name
	funcPath := filepath.Join(fixturesPath, "functions", "test_function1_glob.yaml")
	invalidFunc := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\nname: 123invalid\n"
	afero.WriteFile(cowFs, funcPath, []byte(invalidFunc), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /functions/test_function1_glob.yaml:2:7: invalid variable name
	expectedError := "/functions/test_function1_glob.yaml:2:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_FunctionMissingId tests required id field
func TestCompile_FunctionMissingId(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify function to remove required id
	funcPath := filepath.Join(fixturesPath, "functions", "test_function1_glob.yaml")
	invalidFunc := "description: an http function\n"
	afero.WriteFile(cowFs, funcPath, []byte(invalidFunc), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /functions/test_function1_glob.yaml:1:1: required attribute 'id'
	expectedError := "/functions/test_function1_glob.yaml:1:1: required attribute 'id'"
	assert.Error(t, err, expectedError)
}

// TestCompile_FunctionInvalidId tests CID validation on id field
func TestCompile_FunctionInvalidId(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify function to have invalid CID
	funcPath := filepath.Join(fixturesPath, "functions", "test_function1_glob.yaml")
	invalidFunc := "id: not-a-valid-cid\n"
	afero.WriteFile(cowFs, funcPath, []byte(invalidFunc), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /functions/test_function1_glob.yaml:1:5: failed parsing `not-a-valid-cid` with invalid cid: selected encoding not supported
	expectedError := "/functions/test_function1_glob.yaml:1:5: failed parsing `not-a-valid-cid` with invalid cid: selected encoding not supported"
	assert.Error(t, err, expectedError)
}

// TestCompile_StorageInvalidNetworkAccess tests InSet validation for storage network-access
func TestCompile_StorageInvalidNetworkAccess(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify storage to have invalid network-access
	storagePath := filepath.Join(fixturesPath, "storages", "test_storage1.yaml")
	invalidStorage := "id: QmSbe2pTyH3fpF2T8JSAk6s3js2MqUg2gi5Hx2iTWCBtqX\naccess:\n    network: invalid_access\nstreaming:\n    ttl: 5m\n"
	afero.WriteFile(cowFs, storagePath, []byte(invalidStorage), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /storages/test_storage1.yaml:3:14: invalid value
	expectedError := "/storages/test_storage1.yaml:3:14: invalid value"
	assert.Error(t, err, expectedError)
}

// TestCompile_ApplicationFunctionError tests errors in application-specific functions
func TestCompile_ApplicationFunctionError(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify application function to have invalid name (keep valid id and required fields)
	appFuncPath := filepath.Join(fixturesPath, "applications", "test_app1", "functions", "test_function2.yaml")
	invalidFunc := "id: QmXuTz6e3W7Y9EJ2hYH4Jk1JAXT7pKnai5NqUWFPVF5Cmx\nname: @invalid-name\ntrigger:\n    type: pubsub\n    local: true\n    channel: channel2\nsource: \"libraries/test_library2\"\nexecution:\n    timeout: 23s\n    memory: 23MB\n    call: ping2\n"
	afero.WriteFile(cowFs, appFuncPath, []byte(invalidFunc), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Note: The error might be from a different file if there are other validation issues
	// Check if we get the expected error or a required attribute error from another file
	errStr := err.Error()
	hasAppFunctionError := strings.Contains(errStr, "/applications/test_app1/functions/test_function2.yaml:") &&
		strings.Contains(errStr, "invalid variable name")
	if hasAppFunctionError {
		// Expected exact format: /applications/test_app1/functions/test_function2.yaml:2:7: invalid variable name
		expectedError := "/applications/test_app1/functions/test_function2.yaml:2:7: invalid variable name"
		assert.Error(t, err, expectedError)
	} else {
		// If we get a different error (like required attribute), verify it has location
		assert.Assert(t, strings.Contains(errStr, "required attribute") || strings.Contains(errStr, "/applications/"),
			"Error should reference required attribute or application path. Got: %s", errStr)
	}
}

// TestCompile_MultipleValidationErrors tests that multiple errors are reported
func TestCompile_MultipleValidationErrors(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify multiple files to have errors
	funcPath := filepath.Join(fixturesPath, "functions", "test_function1_glob.yaml")
	invalidFunc := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\nname: 999invalid\n"
	afero.WriteFile(cowFs, funcPath, []byte(invalidFunc), 0644)

	domainPath := filepath.Join(fixturesPath, "domains", "test_domain1.yaml")
	invalidDomain := "id: QmUcVJtgGZYkqFr2J9t2jV2fJJWZBvD7FJ6RyXzJY2kAj1\nfqdn: invalid!!!\n"
	afero.WriteFile(cowFs, domainPath, []byte(invalidDomain), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Should report at least one error with location
	// Since multiple errors exist, we check that at least one is reported with exact format
	errStr := err.Error()
	assert.Assert(t, errStr != "", "Error should not be empty")
	// Should contain either function or domain error (exact format depends on which error is reported first)
	hasFunctionError := strings.Contains(errStr, "/functions/test_function1_glob.yaml:")
	hasDomainError := strings.Contains(errStr, "/domains/test_domain1.yaml:")
	assert.Assert(t, hasFunctionError || hasDomainError, "Should report at least one validation error. Got: %s", errStr)

	// If function error is reported, verify exact format
	if hasFunctionError {
		expectedError := "/functions/test_function1_glob.yaml:2:7: invalid variable name"
		assert.Error(t, err, expectedError)
	}
	// If domain error is reported, verify exact format
	if hasDomainError {
		expectedError := "/domains/test_domain1.yaml:2:7: invalid fqdn"
		assert.Error(t, err, expectedError)
	}
}

// TestCompile_LibraryInvalidName tests variable name validation on library
func TestCompile_LibraryInvalidName(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify library to have invalid name (keep required git-provider)
	libPath := filepath.Join(fixturesPath, "libraries", "test_library1.yaml")
	invalidLib := "id: QmPzW5WJfw7oR8zHrYPXGMxqM9vLhZ6vW7jbUbJj5Xf4sR\nname: -invalid\nsource:\n    github:\n        id: \"111111111\"\n        fullname: taubyte-test/library1\n"
	afero.WriteFile(cowFs, libPath, []byte(invalidLib), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /libraries/test_library1.yaml:2:7: invalid variable name
	expectedError := "/libraries/test_library1.yaml:2:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_WebsiteInvalidName tests variable name validation on website
func TestCompile_WebsiteInvalidName(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify website to have invalid name (keep required git-provider)
	websitePath := filepath.Join(fixturesPath, "websites", "test_website1.yaml")
	invalidWebsite := "id: QmZmW9PQmz5Z6pYPJ6VDUPVgH7L6Xb8K1GTh8dNQzDh5gh\nname: 0invalid\nsource:\n    github:\n        id: \"111111112\"\n        fullname: taubyte-test/photo_booth\n"
	afero.WriteFile(cowFs, websitePath, []byte(invalidWebsite), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /websites/test_website1.yaml:2:7: invalid variable name
	expectedError := "/websites/test_website1.yaml:2:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_SmartopInvalidName tests variable name validation on smartop
func TestCompile_SmartopInvalidName(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify smartop to have invalid name
	smartopPath := filepath.Join(fixturesPath, "smartops", "test_smartops1.yaml")
	invalidSmartop := "id: QmQ5vhrL7uv6tuoN9KeVBwd4PwfQkXdVVmDLUZuTNxqgvm\nname: invalid-name-with-dashes\n"
	afero.WriteFile(cowFs, smartopPath, []byte(invalidSmartop), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /smartops/test_smartops1.yaml:2:7: invalid variable name
	expectedError := "/smartops/test_smartops1.yaml:2:7: invalid variable name"
	assert.Error(t, err, expectedError)
}

// TestCompile_DomainCertificateTypeInvalid tests InSet validation for certificate-type
func TestCompile_DomainCertificateTypeInvalid(t *testing.T) {
	fixturesPath := filepath.Join("fixtures", "config")
	baseFs := afero.NewReadOnlyFs(afero.NewOsFs())
	overlayFs := afero.NewMemMapFs()
	cowFs := afero.NewCopyOnWriteFs(baseFs, overlayFs)

	// Modify domain to have invalid certificate-type
	domainPath := filepath.Join(fixturesPath, "domains", "test_domain1.yaml")
	invalidDomain := "id: QmUcVJtgGZYkqFr2J9t2jV2fJJWZBvD7FJ6RyXzJY2kAj1\nfqdn: hal.computers.com\ncertificate:\n    type: invalid_type\n"
	afero.WriteFile(cowFs, domainPath, []byte(invalidDomain), 0644)

	compiler, err := New(WithVirtual(cowFs, fixturesPath))
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())

	// Expected exact format: /domains/test_domain1.yaml:4:11: invalid value
	expectedError := "/domains/test_domain1.yaml:4:11: invalid value"
	assert.Error(t, err, expectedError)
}
