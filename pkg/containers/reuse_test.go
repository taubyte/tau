package containers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPulledWithin(t *testing.T) {
	name := "example/pulled-within:test"
	defer lastPulled.Delete(name)

	assert.False(t, pulledWithin(name, time.Hour), "never pulled")

	markPulled(name)
	assert.True(t, pulledWithin(name, time.Hour))
	assert.False(t, pulledWithin(name, 0), "no reuse window means always pull")

	lastPulled.Store(name, time.Now().Add(-2*time.Hour))
	assert.False(t, pulledWithin(name, time.Hour), "pulled too long ago")
}
