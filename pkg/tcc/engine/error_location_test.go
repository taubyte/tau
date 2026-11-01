package engine

import (
	"errors"
	"net/mail"
	"strings"
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestValidationError_ReportsLine1Column1(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	afero.WriteFile(fs, "/test/email.yaml", []byte("invalid-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	emailOnly()(attr)

	node := &Node{
		Group:      false,
		Attributes: []*Attribute{attr},
	}

	query := sr.Query()
	_, err = load(node, query, walk{})

	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Error should be reported at line 1, column 1 in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Error should reference email.yaml file")
}

func TestValidationError_CarriesLocationStructurally(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, fs.MkdirAll("/test", 0755))
	assert.NilError(t, afero.WriteFile(fs, "/test/email.yaml", []byte("invalid-email"), 0644))

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{Name: "email", Type: TypeString, Required: true}
	emailOnly()(attr)
	_, err = load(&Node{Attributes: []*Attribute{attr}}, sr.Query(), walk{})
	assert.Assert(t, err != nil)

	var le *LocatedError
	assert.Assert(t, errors.As(err, &le), "compile errors must carry their location")
	assert.Equal(t, le.File, "/email.yaml")
	assert.Equal(t, le.Line, 1)
	assert.Equal(t, le.Column, 1)
	// ...and render exactly as before, so message-matching callers are unaffected.
	assert.Equal(t, err.Error(), "/email.yaml:1:1: "+le.Err.Error())
}

func TestValidationError_MultiLineYAML_ReportsCorrectLine(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	afero.WriteFile(fs, "/test/name.yaml", []byte("test-name"), 0644)
	afero.WriteFile(fs, "/test/count.yaml", []byte("42"), 0644)
	afero.WriteFile(fs, "/test/email.yaml", []byte("invalid-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	emailAttr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	emailOnly()(emailAttr)

	node := &Node{
		Group: false,
		Attributes: []*Attribute{
			{Name: "name", Type: TypeString},
			{Name: "count", Type: TypeInt},
			emailAttr,
		},
	}

	query := sr.Query()
	_, err = load(node, query, walk{})

	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Error should be at line 1, column 1 in email.yaml in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Error should reference email.yaml, not other files")
}

func TestRequiredAttributeError_ReportsLocation(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	yamlContent := "name: test\n"
	afero.WriteFile(fs, "/test/config.yaml", []byte(yamlContent), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	node := &Node{
		Group: true,
		Leaf:  "config", // the document this group's fields live in, which the schema names
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
	_, err = load(node, query, walk{})

	assert.ErrorContains(t, err, "required attribute")
	errStr := err.Error()

	assert.Assert(t, strings.Contains(errStr, ":"), "Error must contain location information in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "config.yaml", "Error should reference config.yaml file")
	assert.ErrorContains(t, err, "required attribute 'requiredField'", "Error should contain required attribute message")
}

func TestErrorFormat_ContainsFileLineColumn(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	afero.WriteFile(fs, "/test/email.yaml", []byte("not-an-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	emailOnly()(attr)

	node := &Node{
		Group:      false,
		Attributes: []*Attribute{attr},
	}

	query := sr.Query()
	_, err = load(node, query, walk{})

	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Error must contain location in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Error must contain file path")
}

func TestNestedFileError_ReportsCorrectPath(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/nested", 0755)

	afero.WriteFile(fs, "/test/nested/email.yaml", []byte("bad-email"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	emailOnly()(attr)

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
	_, err = load(node, query, walk{})

	assert.Assert(t, strings.Contains(err.Error(), ":"), "Error must contain location information in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "nested", "Error should reference nested path containing 'nested'")
}

func TestWrappedError_PreservesLocation(t *testing.T) {
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)

	afero.WriteFile(fs, "/test/email.yaml", []byte("invalid"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	attr := &Attribute{
		Name:     "email",
		Type:     TypeString,
		Required: true,
	}
	emailOnly()(attr)

	node := &Node{
		Group:      false,
		Attributes: []*Attribute{attr},
	}

	query := sr.Query()
	_, err = load(node, query, walk{})

	assert.ErrorContains(t, err, "/email.yaml:1:1:", "Wrapped error must preserve location at line 1, column 1 in format 'filepath:line:column'")
	assert.ErrorContains(t, err, "email.yaml", "Wrapped error must preserve file path")
}

func emailOnly() Option {
	return Validator(func(s string) error {
		_, err := mail.ParseAddress(s)
		return err
	})
}
