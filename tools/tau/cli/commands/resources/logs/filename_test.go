package logs

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestLogFileNameIsValidOnWindows(t *testing.T) {
	assert.Equal(t, logFileName("2026-11-07T16:27:54Z"), "2026-11-07T16_27_54Z")
	assert.Equal(t, logFileName(`a/..\b`), "a_.._b")
	assert.Equal(t, logFileName("j1"), "j1")
}
