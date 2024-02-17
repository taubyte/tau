package app

import (
	"testing"

	"gotest.tools/v3/assert"
)

var (
	testUrl       = "example.test.com"
	expectedRegex = `^([^.]+\.)?tau\.example\.test\.com$`
)

func TestProtocolsRegex(t *testing.T) {
	url := convertToProtocolsRegex(testUrl)
	assert.Equal(t, url, expectedRegex)

}
