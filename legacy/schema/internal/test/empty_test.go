package internal

import (
	"fmt"
	"testing"

	"gotest.tools/v3/assert"
)

func TestIsEmpty(t *testing.T) {
	type testStruct struct {
		foo string
	}

	var testCases = []struct {
		value interface{}
		empty interface{}
	}{
		{
			value: "string",
			empty: "",
		},
		{
			value: []string{"a", "b", "c"},
			empty: []string{},
		},
		{
			value: 1,
			empty: 0,
		},
		{
			value: 1.0,
			empty: 0.0,
		},
		{
			value: true,
			empty: false,
		},
		{
			value: testStruct{foo: "bar"},
			empty: testStruct{},
		},
		{
			value: map[string]string{"a": "b", "c": "d"},
			empty: map[string]string{},
		},
		{
			value: map[int]int{1: 2, 3: 4},
			empty: map[int]int{},
		},
		{
			value: map[interface{}]interface{}{"a": 1, "b": 2},
			empty: map[interface{}]interface{}{},
		},
		{
			value: map[string]testStruct{"a": {foo: "bar"}, "b": {foo: "baz"}},
			empty: map[string]testStruct{},
		},
		{
			value: map[testStruct]testStruct{{foo: "bar"}: {foo: "baz"}},
			empty: map[testStruct]testStruct{},
		},
	}

	for _, testCase := range testCases {
		t.Run(fmt.Sprintf("%T", testCase.value), func(t *testing.T) {
			assert.Assert(t, !isEmpty(testCase.value))
			assert.Assert(t, isEmpty(testCase.empty))
		})
	}
}
