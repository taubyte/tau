package basic_test

import (
	"fmt"
	"testing"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

type brokenIface struct {
	basic.ResourceIface
}

func (brokenIface) SetName(name string) {
}

func (brokenIface) WrapError(format string, i ...any) error {
	return fmt.Errorf(format, i...)
}

func TestDeleteErrors(t *testing.T) {
	_seer, err := internal.NewSeer()
	assert.NilError(t, err)

	r := &basic.Resource{
		ResourceIface: brokenIface{},
		Root:          func() *seer.Query { return _seer.Get("test").Delete() },
		Config:        func() *seer.Query { return _seer.Get("test").Document() },
	}

	err = r.Delete("non-exist")
	assert.ErrorContains(t, err, "Is this a Document?")

	err = r.Delete()
	assert.ErrorContains(t, err, "Is this a Document?")
}
