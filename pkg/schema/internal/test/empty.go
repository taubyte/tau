package internal

import (
	"fmt"
	"reflect"
	"testing"

	"gotest.tools/v3/assert"
)

func isEmpty(val interface{}) bool {
	switch v := val.(type) {
	case string:
		return v == ""
	case int, int16, int32, int64, int8:
		return v == 0
	case uint, uint16, uint32, uint64, uint8:
		return v == 0
	case float32, float64:
		return v == 0.0
	case bool:
		return !v
	default:
		reflectVal := reflect.ValueOf(val)
		switch reflectVal.Kind() {
		case reflect.Array, reflect.Map, reflect.Slice:
			return reflectVal.Len() == 0
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Ptr:
			return reflectVal.IsNil()
		case reflect.Struct:
			return reflectVal.IsZero()
		default:
			return false
		}
	}
}

// PR created on gotest.tools https://github.com/gotestyourself/gotest.tools/pull/251
func AssertEmpty(t *testing.T, values ...any) {
	for _, val := range values {
		assert.Assert(t, isEmpty(val), fmt.Sprintf("%T (%#v) is not empty", val, val))
	}
}
