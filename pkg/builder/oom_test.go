package builder

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestOutOfMemoryMessageNamesTheLimit(t *testing.T) {
	msg := OutOfMemoryError{Limit: 4 << 30}.Error()
	assert.Assert(t, strings.Contains(msg, "ran out of memory"), msg)
	assert.Assert(t, strings.Contains(msg, "4.0 GiB"), msg)

	assert.Assert(t, !strings.Contains(msg, "swap"), msg)

	small := OutOfMemoryError{Limit: 1 << 30, Swap: true}.Error()
	assert.Assert(t, strings.Contains(small, "1.0 GiB of RAM plus the node's swap"), small)
	assert.Assert(t, strings.Contains(small, "more memory or swap"), small)

	assert.Assert(t, strings.Contains(OutOfMemoryError{}.Error(), "the machine"))
	assert.Equal(t, (&builder{dev: true}).outOfMemory(), OutOfMemoryError{})
}
