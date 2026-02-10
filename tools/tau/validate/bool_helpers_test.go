package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestIsAny(t *testing.T) {
	alwaysTrue := func(string) bool { return true }
	alwaysFalse := func(string) bool { return false }

	assert.Equal(t, validate.IsAny("x", alwaysTrue), true)
	assert.Equal(t, validate.IsAny("x", alwaysFalse), false)
	assert.Equal(t, validate.IsAny("x", alwaysFalse, alwaysTrue), true)
	assert.Equal(t, validate.IsAny("x", alwaysFalse, alwaysFalse), false)
}

func TestIsInt(t *testing.T) {
	assert.Equal(t, validate.IsInt("0"), true)
	assert.Equal(t, validate.IsInt("42"), true)
	assert.Equal(t, validate.IsInt("-1"), true)
	assert.Equal(t, validate.IsInt(""), false)
	assert.Equal(t, validate.IsInt("abc"), false)
	assert.Equal(t, validate.IsInt("12.3"), false)
}

func TestIsBytes(t *testing.T) {
	assert.Equal(t, validate.IsBytes("0"), true)
	assert.Equal(t, validate.IsBytes("1KB"), true)
	assert.Equal(t, validate.IsBytes("10GB"), true)
	assert.Equal(t, validate.IsBytes("invalid"), false)
}
