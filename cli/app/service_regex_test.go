package app

import (
	"testing"

	"gotest.tools/v3/assert"
)

var (
	testUrl       = "example.test.com"
	expectedRegex = `^[^.]+\.tau\.example\.test\.com`
)

func TestServiceRegex(t *testing.T) {
	url := convertToServiceRegex(testUrl)
	assert.Equal(t, url, expectedRegex)

}
