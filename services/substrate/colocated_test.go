package substrate

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestCoLocatedServices(t *testing.T) {
	assert.Equal(t, len(coLocatedServices(nil)), 0)
	assert.Equal(t, len(coLocatedServices([]string{"substrate"})), 0)
	assert.DeepEqual(t,
		coLocatedServices([]string{"auth", "tns", "hoarder", "seer", "substrate", "patrick", "monkey"}),
		[]string{"auth", "tns", "hoarder", "seer", "patrick", "monkey"})
}
