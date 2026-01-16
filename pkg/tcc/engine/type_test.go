package engine

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestTypeString(t *testing.T) {
	tests := []struct {
		typ    Type
		expect string
	}{
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
