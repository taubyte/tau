package app

import (
	"testing"

	"gotest.tools/v3/assert"
)

var (
	testUrl       = "example.test.com"
	expectedRegex = `^[^.]+\.tau\.example\.test\.com$`
)

func TestServicesRegex(t *testing.T) {
	url := convertToServicesRegex(testUrl)
	assert.Equal(t, url, expectedRegex)

}
