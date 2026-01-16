package compiler

import (
	"context"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

var fakeMeta = patrick.Meta{
	Repository: patrick.Repository{
		Provider: "github",
		Branch:   "master",
		ID:       12356,
	},
	HeadCommit: patrick.HeadCommit{
		ID: "345690",
	},
}

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestCompile(t *testing.T) {
	project, err := projectLib.Open(projectLib.SystemFS("fixtures/config"))
	assert.NilError(t, err)

	rc, err := compile.CompilerConfig(project, fakeMeta, generatedDomainRegExp)
	assert.NilError(t, err)

	oldCompiler, err := compile.New(rc, compile.Dev())
	assert.NilError(t, err)

	err = oldCompiler.Build()
	assert.NilError(t, err)

	compiler, err := New(WithLocal("fixtures/config"), WithBranch("master"))
	assert.NilError(t, err)

	obj, validations, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	newObj := obj.Flat()["object"].(map[string]interface{})
	oldObj := oldCompiler.Object()

	assert.Assert(t, cmp.Equal(newObj, oldObj), cmp.Diff(oldObj, newObj))

	indexes := obj.Flat()["indexes"].(map[string]interface{})

	// older compiler has a bug where it does not handle messaging inside an app
	// delete it to make the deep equal works
	delete(indexes, "p2p/pubsub/QmUgRE95oaisf5cK1DNaKizPQS7mqtd3zZ68wuUEKfoWoB")

	assert.Assert(t, cmp.Equal(indexes, oldCompiler.Indexes()), cmp.Diff(oldCompiler.Indexes(), indexes))

	// Verify validations are returned
	assert.Assert(t, validations != nil, "validations should not be nil")

	// Strictly verify domain validations exist (fixtures/config/domains/test_domain1.yaml exists)
	domainValidations := []engine.NextValidation{}
	for _, v := range validations {
		if v.Validator == "dns" && v.Key == "domain" {
			domainValidations = append(domainValidations, v)
		}
	}

	// Must have at least one domain validation (test_domain1.yaml with fqdn: hal.computers.com)
	assert.Assert(t, len(domainValidations) > 0, "expected at least one domain validation, got %d", len(domainValidations))

	// Strictly verify the global domain validation from test_domain1.yaml exists
	foundGlobalDomain := false
	expectedGlobalFQDN := "hal.computers.com"
	expectedProjectID := "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"

	for _, v := range domainValidations {
		fqdn, ok := v.Value.(string)
		assert.Assert(t, ok, "domain validation value should be a string (FQDN)")

		// Verify context has project
		project, ok := v.Context["project"].(string)
		assert.Assert(t, ok, "context should have project as string")
		assert.Assert(t, len(project) > 0, "project should not be empty")
		assert.Equal(t, project, expectedProjectID, "project ID should match config.yaml")

		if fqdn == expectedGlobalFQDN {
			// Check if this is the global domain (no app context)
			_, hasApp := v.Context["app"]
			if !hasApp {
				foundGlobalDomain = true
				// All validations should have proper structure
				assert.Equal(t, v.Key, "domain", "domain validation key should be 'domain'")
				assert.Equal(t, v.Validator, "dns", "domain validation validator should be 'dns'")
			}
		}
	}

	// Strict requirement: global domain must exist
	assert.Assert(t, foundGlobalDomain, "expected to find global domain validation for %s from fixtures/config/domains/test_domain1.yaml", expectedGlobalFQDN)

}

func TestCompile_ReturnsValidations(t *testing.T) {
	compiler, err := New(WithLocal("fixtures/config"), WithBranch("master"))
	assert.NilError(t, err)

	obj, validations, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	// Verify object is returned
	assert.Assert(t, obj != nil)

	// Verify validations is returned (should be a slice, not nil)
	assert.Assert(t, validations != nil, "validations should not be nil")
	assert.Assert(t, len(validations) >= 0, "validations should be a valid slice")

	// If there are validations, verify their structure
	for i, v := range validations {
		// Verify required fields are present
		assert.Assert(t, v.Key != "", "validation[%d].Key should not be empty", i)
		assert.Assert(t, v.Validator != "", "validation[%d].Validator should not be empty", i)
		assert.Assert(t, v.Value != nil, "validation[%d].Value should not be nil", i)
		assert.Assert(t, v.Context != nil, "validation[%d].Context should not be nil", i)

		// Verify context structure for domain validations
		if v.Validator == "dns" && v.Key == "domain" {
			// Value should be a string (FQDN)
			fqdn, ok := v.Value.(string)
			assert.Assert(t, ok, "validation[%d].Value should be a string for DNS validation", i)
			assert.Assert(t, len(fqdn) > 0, "validation[%d].Value (FQDN) should not be empty", i)

			// Context should have project
			project, ok := v.Context["project"].(string)
			assert.Assert(t, ok, "validation[%d].Context should have project as string", i)
			assert.Assert(t, len(project) > 0, "validation[%d].Context.project should not be empty", i)

			// App is optional, but if present should be a string
			if app, exists := v.Context["app"]; exists {
				_, ok := app.(string)
				assert.Assert(t, ok, "validation[%d].Context.app should be a string if present", i)
			}
		}
	}
}
