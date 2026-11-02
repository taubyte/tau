package engine

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestTypeString(t *testing.T) {
	type testCase struct {
		typ    Type
		expect string
	}
	tests := []testCase{
		{TypeInt, "Int"},
		{TypeBool, "Bool"},
		{TypeFloat, "Float"},
		{TypeString, "String"},
		{TypeStringSlice, "StringSlice"},
	}

	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			assert.Equal(t, tt.typ.String(), tt.expect)
		})
	}
}
