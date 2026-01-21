package pass4

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestPipe_ReturnsAllTransformers(t *testing.T) {
	// Execute: Get pipe transformers
	transformers := Pipe("main")

	// Verify: Should contain all 9 transformers (InitIndexes + 8 resource transformers)
	assert.Equal(t, len(transformers), 9)

	// Verify transformers are not nil
	for i, transformer := range transformers {
		assert.Assert(t, transformer != nil, "transformer %d is nil", i)
	}
}
