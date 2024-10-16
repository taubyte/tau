package basic_test

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
	"gotest.tools/v3/assert"
)

func TestSetErrors(t *testing.T) {
	fs := afero.NewMemMapFs()
	_seer, err := seer.New(seer.VirtualFS(fs, "/"))
	assert.NilError(t, err)

	r, err := basic.New(_seer, brokenIface{}, "test")
	assert.NilError(t, err)

	err = r.Set(true, func(ci basic.ConfigIface) []*seer.Query {
		return []*seer.Query{_seer.Get("test").Set(0)}
	})
	assert.ErrorContains(t, err, "committing failed with failed to call Set() outside a document")

	// Set a value
	err = r.Set(true, func(ci basic.ConfigIface) []*seer.Query {
		return []*seer.Query{_seer.Get("test").Document().Get("value").Set(0)}
	})
	assert.NilError(t, err)

	// Create a read only fs
	_seer, err = seer.New(seer.VirtualFS(afero.NewReadOnlyFs(fs), "/"))
	assert.NilError(t, err)

	r, err = basic.New(_seer, brokenIface{}, "test")
	assert.NilError(t, err)

	// Attempt to reset the value on read only fs
	err = r.Set(true, func(ci basic.ConfigIface) []*seer.Query {
		return []*seer.Query{_seer.Get("test").Document().Get("value").Set(1)}
	})
	assert.ErrorContains(t, err, "sync failed with: opening /test.yaml failed with operation not permitted")
}
