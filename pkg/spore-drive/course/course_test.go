package course

import (
	"testing"

	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	"github.com/taubyte/tau/pkg/spore-drive/mycelium"
	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	_, p := fixtures.VirtConfig()
	n, err := mycelium.Map(p)
	assert.NilError(t, err)
	_, err = New(n)
	assert.NilError(t, err)
}

func TestPlot(t *testing.T) {
	_, p := fixtures.VirtConfig()
	n, err := mycelium.Map(p)
	assert.NilError(t, err)
	c, err := New(n, Shape("shape1"))
	assert.NilError(t, err)

	plot := c.Hyphae()

	assert.Equal(t, plot.Size(), 2)

	assert.Equal(t, len(plot), 1)
}

func TestPlotMulti(t *testing.T) {
	_, p := fixtures.VirtConfig()
	n, err := mycelium.Map(p)
	assert.NilError(t, err)
	c, err := New(n, Shapes("shape1", "shape2"))
	assert.NilError(t, err)

	plot := c.Hyphae()

	assert.Equal(t, plot.Size(), 4)

	assert.Equal(t, len(plot), 2)
}
