package common_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/common"
	"gotest.tools/v3/assert"
)

func TestMapperError(t *testing.T) {
	m := common.Mapper{}

	err := m.Run("invalid")
	assert.ErrorContains(t, err, "invalid type: string, expected struct or ptr to struct")

	err = m.Run(new(string))
	assert.ErrorContains(t, err, "string is not a struct")
}
