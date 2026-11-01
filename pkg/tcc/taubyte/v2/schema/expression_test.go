package schema_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

func TestExpressionSyntaxIsChecked(t *testing.T) {
	check := validatorOf(t, schema.IsExpression())

	for _, ok := range []string{
		"",                                       // absent, which Required() is the judge of, not this
		"order.total",                            // a selector
		"order.total > 100",                      // a predicate
		"error.code == 429 || error.code >= 500", // the retry-arm case
		"len(order.lines) > 0",
		"filter(order.lines, .qty > 0)", // a builtin over a list
		"order.coupon ?? 'none'",        // nil coalescing
	} {
		assert.NilError(t, check(ok), "rejected %q", ok)
	}

	for _, bad := range []string{
		"order.total >",
		"1 +",
		"'unterminated",
		"filter(order.lines, ",
		"order.total === 100",
	} {
		err := check(bad)
		assert.Assert(t, err != nil, "accepted %q", bad)
		assert.Assert(t, strings.Contains(err.Error(), "invalid expression"),
			"%q said %q", bad, err)
	}

	// AND THE FAULT SAYS WHERE, as an offset into the value the author wrote.
	err := check("1 +")
	assert.Assert(t, strings.Contains(err.Error(), "at character"), "no position in %q", err)
}

func TestExpressionFaultPointsIntoTheYaml(t *testing.T) {
	root := engine.Root(nil,
		engine.DefineGroup("thing",
			engine.DefineIter([]*engine.Attribute{
				engine.String("id", engine.Required()),
				engine.String("name"),
				engine.String("when", schema.IsExpression()),
			}))).With(engine.Leaf("project"), engine.Identity("id", "name"))

	fs := afero.NewMemMapFs()
	//                 123456789012345678
	//                 when: order.total >
	//                                   ^ the dangling operator, column 19
	assert.NilError(t, afero.WriteFile(fs, "/thing/one.yaml", []byte(
		"id: one\n"+
			"name: one\n"+
			"when: order.total >\n"), 0o644))

	c, err := interp.New(engine.SchemaDefinition(root), root, interp.WithVirtual(fs, "/"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.Assert(t, err != nil, "a broken expression compiled clean")

	path, line, column, ok := schema.ExpressionAt(err)
	assert.Assert(t, ok, "the fault does not carry a position: %v", err)
	assert.Equal(t, path, "/thing/one.yaml")
	assert.Equal(t, line, 3)
	assert.Equal(t, column, 19, "the column has to land on the operator with nothing after it: %v", err)

	var located *engine.LocatedError
	assert.Assert(t, errors.As(err, &located), "no located error: %v", err)
	assert.Equal(t, located.Column, 7, "the engine still points at the start of the value")
}

// validatorOf pulls the validator an Option installed, so a check can be exercised
// without building a whole schema around it.
func validatorOf(t *testing.T, opt engine.Option) func(string) error {
	t.Helper()
	a := engine.String("probe", opt)
	assert.Assert(t, a.Validator != nil, "the option installed no validator")
	return func(s string) error { return a.Validator(s) }
}
