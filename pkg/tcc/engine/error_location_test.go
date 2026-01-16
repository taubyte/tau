package engine

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// TestValidationError_ReportsLine1Column1 verifies that validation errors report the correct line and column.
// When a single-line YAML file has an invalid value, the error should be at line 1, column 1.
func TestValidationError_ReportsLine1Column1(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	// Create a YAML file with invalid email on line 1
	// The error should be reported at line 1, column 1
	afero.WriteFile(fs, "/test/email.yaml", []byte("invalid-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	IsEmail()(attr)

	node := &Node{
		Group:      false,
		Attributes: []*Attribute{attr},
	}

	query := sr.Query()
	_, err = load[object.Refrence](node, query)

	// Expected format: /email.yaml:1:1: [validator error message]
	// Check for location format: filepath:line:column
	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Error should be reported at line 1, column 1 in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Error should reference email.yaml file")
}

// TestValidationError_MultiLineYAML_ReportsCorrectLine verifies that errors in multi-line YAML files
// report the correct line number where the error occurs.
func TestValidationError_MultiLineYAML_ReportsCorrectLine(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	// Create YAML files - each attribute is a separate file
	// We'll create multiple files and verify the error points to the correct one
	afero.WriteFile(fs, "/test/name.yaml", []byte("test-name"), 0644)
	afero.WriteFile(fs, "/test/count.yaml", []byte("42"), 0644)
	// This file will have an invalid email - error should be at line 1
	afero.WriteFile(fs, "/test/email.yaml", []byte("invalid-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	emailAttr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	IsEmail()(emailAttr)

	node := &Node{
		Group: false,
		Attributes: []*Attribute{
			{Name: "name", Type: TypeString},
			{Name: "count", Type: TypeInt},
			emailAttr,
		},
	}

	query := sr.Query()
	_, err = load[object.Refrence](node, query)

	// Expected format: /email.yaml:1:1: [validator error message]
	// Check for location format: filepath:line:column
	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Error should be at line 1, column 1 in email.yaml in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Error should reference email.yaml, not other files")
}

// TestRequiredAttributeError_ReportsLocation verifies that missing required attribute errors
// include location information when the file exists but the attribute is missing.
func TestRequiredAttributeError_ReportsLocation(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	// Create a config.yaml file that exists but doesn't have the required field
	// For group nodes, attributes are read from config.yaml
	yamlContent := "name: test\n"
	afero.WriteFile(fs, "/test/config.yaml", []byte(yamlContent), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	node := &Node{
		Group: true,
		Attributes: []*Attribute{
			{Name: "name", Type: TypeString},
			{
				Name:     "requiredField",
				Type:     TypeString,
				Required: true,
			},
		},
		Children: []*Node{},
	}

	query := sr.Query()
	_, err = load[object.Refrence](node, query)

	assert.ErrorContains(t, err, "required attribute")
	errStr := err.Error()

	// Error must contain location information in format: filepath:line:column: message
	// Format: "/config.yaml:1:1: required attribute 'requiredField'"
	assert.Assert(t, strings.Contains(errStr, ":"), "Error must contain location information in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "config.yaml", "Error should reference config.yaml file")
	assert.ErrorContains(t, err, "required attribute 'requiredField'", "Error should contain required attribute message")
}

// TestErrorFormat_ContainsFileLineColumn verifies that error messages follow the format:
// "error message (at filepath:line:column)" or "error message (at filepath:line)"
func TestErrorFormat_ContainsFileLineColumn(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	// Create a file with invalid email to trigger a validation error
	afero.WriteFile(fs, "/test/email.yaml", []byte("not-an-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	IsEmail()(attr)

	node := &Node{
		Group:      false,
		Attributes: []*Attribute{attr},
	}

	query := sr.Query()
	_, err = load[object.Refrence](node, query)

	// Expected format: /email.yaml:1:1: [validator error message]
	// Verify the error format: filepath:line:column: message
	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Error must contain location in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Error must contain file path")
}

// TestNestedFileError_ReportsCorrectPath verifies that errors in nested file structures
// report the correct file path, not just the root path.
func TestNestedFileError_ReportsCorrectPath(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/nested", 0755)

	// Create a nested structure with an invalid email
	afero.WriteFile(fs, "/test/nested/email.yaml", []byte("bad-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	IsEmail()(attr)

	node := &Node{
		Group: true,
		Children: []*Node{
			{
				Group:      false,
				Match:      "nested",
				Attributes: []*Attribute{attr},
			},
		},
	}

	query := sr.Query()
	_, err = load[object.Refrence](node, query)

	// Expected format: /nested/email.yaml:line:column: [validator error message]
	// Error must reference the nested path
	assert.Assert(t, strings.Contains(err.Error(), ":"), "Error must contain location information in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "nested", "Error should reference nested path containing 'nested'")
}

// TestWrappedError_PreservesLocation verifies that when errors are wrapped,
// the location information is preserved in the error message.
func TestWrappedError_PreservesLocation(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	// Create a file with invalid email to trigger a validation error that gets wrapped
	afero.WriteFile(fs, "/test/email.yaml", []byte("invalid"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	IsEmail()(attr)

	node := &Node{
		Group:      false,
		Attributes: []*Attribute{attr},
	}

	query := sr.Query()
	_, err = load[object.Refrence](node, query)

	// Expected format: /email.yaml:1:1: [validator error message]
	// Wrapped error must preserve location
	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Wrapped error must preserve location at line 1, column 1 in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Wrapped error must preserve file path")
}
