package starlark

import (
	"reflect"
	"testing"

	"go.starlark.net/starlark"
	"gotest.tools/v3/assert"
)

func TestConvertFromStarlarkBasedOnValue(t *testing.T) {
	t.Run("primitives", func(t *testing.T) {
		intVal := convertFromStarlarkBasedOnValue(starlark.MakeInt(42))
		assert.Equal(t, intVal, int64(42))

		floatVal := convertFromStarlarkBasedOnValue(starlark.Float(3.14))
		assert.Equal(t, floatVal, 3.14)

		stringVal := convertFromStarlarkBasedOnValue(starlark.String("hello"))
		assert.Equal(t, stringVal, "hello")

		boolVal := convertFromStarlarkBasedOnValue(starlark.Bool(true))
		assert.Equal(t, boolVal, true)
	})

	t.Run("list recursion", func(t *testing.T) {
		nestedList := starlark.NewList([]starlark.Value{starlark.Bool(true)})
		list := starlark.NewList([]starlark.Value{
			starlark.MakeInt(1),
			starlark.String("two"),
			nestedList,
		})

		converted, ok := convertFromStarlarkBasedOnValue(list).([]any)
		if !ok {
			t.Fatalf("expected []any, got %T", converted)
		}

		assert.Equal(t, converted[0], int64(1))
		assert.Equal(t, converted[1], "two")

		nested, ok := converted[2].([]any)
		if !ok {
			t.Fatalf("expected nested []any, got %T", converted[2])
		}
		assert.Equal(t, nested[0], true)
	})

	t.Run("dict recursion", func(t *testing.T) {
		list := starlark.NewList([]starlark.Value{starlark.MakeInt(3), starlark.String("four")})

		dict := starlark.NewDict(0)
		assert.NilError(t, dict.SetKey(starlark.String("answer"), starlark.MakeInt(42)))
		assert.NilError(t, dict.SetKey(starlark.String("numbers"), list))
		assert.NilError(t, dict.SetKey(starlark.MakeInt(1), starlark.String("one")))

		converted, ok := convertFromStarlarkBasedOnValue(dict).(map[any]any)
		if !ok {
			t.Fatalf("expected map[any]any, got %T", converted)
		}

		assert.Equal(t, converted["answer"], int64(42))
		numbers, ok := converted["numbers"].([]any)
		if !ok {
			t.Fatalf("expected numbers to be []any, got %T", converted["numbers"])
		}
		assert.DeepEqual(t, numbers, []any{int64(3), "four"})
		assert.Equal(t, converted[int64(1)], "one")
	})

	t.Run("none value", func(t *testing.T) {
		assert.Equal(t, convertFromStarlarkBasedOnValue(starlark.None), nil)
	})

	t.Run("default passthrough", func(t *testing.T) {
		tuple := starlark.Tuple{starlark.MakeInt(7)}
		out := convertFromStarlarkBasedOnValue(tuple)

		resultTuple, ok := out.(starlark.Tuple)
		if !ok {
			t.Fatalf("expected starlark.Tuple, got %T", out)
		}
		assert.Equal(t, len(resultTuple), len(tuple))
		assert.Equal(t, resultTuple[0].Type(), tuple[0].Type())
		assert.Equal(t, resultTuple[0].String(), tuple[0].String())
	})
}

func TestConvertToStarlarkSuccess(t *testing.T) {
	intVal, err := convertToStarlark(21)
	assert.NilError(t, err)
	intFromValue, convErr := starlark.AsInt32(intVal)
	assert.NilError(t, convErr)
	assert.Equal(t, intFromValue, 21)

	uintVal, err := convertToStarlark(uint(42))
	assert.NilError(t, err)
	uintIntVal, ok := uintVal.(starlark.Int)
	assert.Assert(t, ok, "expected starlark.Int, got %T", uintVal)
	uintFromValue, _ := uintIntVal.Int64()
	assert.Equal(t, uintFromValue, int64(42))

	int64Val, err := convertToStarlark(int64(100))
	assert.NilError(t, err)
	int64IntVal, ok := int64Val.(starlark.Int)
	assert.Assert(t, ok, "expected starlark.Int, got %T", int64Val)
	int64FromValue, _ := int64IntVal.Int64()
	assert.Equal(t, int64FromValue, int64(100))

	uint64Val, err := convertToStarlark(uint64(200))
	assert.NilError(t, err)
	uint64IntVal, ok := uint64Val.(starlark.Int)
	assert.Assert(t, ok, "expected starlark.Int, got %T", uint64Val)
	uint64FromValue, _ := uint64IntVal.Uint64()
	assert.Equal(t, uint64FromValue, uint64(200))

	floatVal, err := convertToStarlark(1.5)
	assert.NilError(t, err)
	assert.Equal(t, float64(floatVal.(starlark.Float)), 1.5)

	stringVal, err := convertToStarlark("hello")
	assert.NilError(t, err)
	assert.Equal(t, string(stringVal.(starlark.String)), "hello")

	boolVal, err := convertToStarlark(true)
	assert.NilError(t, err)
	assert.Equal(t, bool(boolVal.(starlark.Bool)), true)

	listVal, err := convertToStarlark([]interface{}{1, "two", []interface{}{false, nil}})
	assert.NilError(t, err)

	list, ok := listVal.(*starlark.List)
	if !ok {
		t.Fatalf("expected *starlark.List, got %T", listVal)
	}
	assert.Equal(t, list.Len(), 3)

	firstInt, _ := starlark.AsInt32(list.Index(0))
	assert.Equal(t, int(firstInt), 1)

	assert.Equal(t, string(list.Index(1).(starlark.String)), "two")

	nestedVal := list.Index(2)
	nestedList, ok := nestedVal.(*starlark.List)
	if !ok {
		t.Fatalf("expected nested *starlark.List, got %T", nestedVal)
	}
	assert.Equal(t, nestedList.Len(), 2)
	assert.Equal(t, bool(nestedList.Index(0).(starlark.Bool)), false)
	assert.Equal(t, nestedList.Index(1), starlark.None)

	inputMap := map[interface{}]interface{}{
		"answer":  42,
		"nested":  []interface{}{true},
		7:         "seven",
		"nilVal":  nil,
		"float64": 3.5,
	}

	dictVal, err := convertToStarlark(inputMap)
	assert.NilError(t, err)

	dict, ok := dictVal.(*starlark.Dict)
	if !ok {
		t.Fatalf("expected *starlark.Dict, got %T", dictVal)
	}

	answer, found, err := dict.Get(starlark.String("answer"))
	assert.NilError(t, err)
	assert.Assert(t, found)
	answerInt, _ := starlark.AsInt32(answer)
	assert.Equal(t, int(answerInt), 42)

	nested, found, err := dict.Get(starlark.String("nested"))
	assert.NilError(t, err)
	assert.Assert(t, found)
	nestedList, ok = nested.(*starlark.List)
	if !ok {
		t.Fatalf("expected nested value to be *starlark.List, got %T", nested)
	}
	assert.Equal(t, nestedList.Len(), 1)
	assert.Equal(t, bool(nestedList.Index(0).(starlark.Bool)), true)

	intKeyVal, found, err := dict.Get(starlark.MakeInt(7))
	assert.NilError(t, err)
	assert.Assert(t, found)
	assert.Equal(t, string(intKeyVal.(starlark.String)), "seven")

	nilVal, found, err := dict.Get(starlark.String("nilVal"))
	assert.NilError(t, err)
	assert.Assert(t, found)
	assert.Equal(t, nilVal, starlark.None)

	floatValFromMap, found, err := dict.Get(starlark.String("float64"))
	assert.NilError(t, err)
	assert.Assert(t, found)
	assert.Equal(t, float64(floatValFromMap.(starlark.Float)), 3.5)
}

func TestConvertToStarlarkMapStringInterface(t *testing.T) {
	inputMap := map[string]interface{}{
		"answer":  42,
		"nested":  []interface{}{true, "test"},
		"nilVal":  nil,
		"float64": 3.5,
		"string":  "hello",
		"bool":    true,
	}

	dictVal, err := convertToStarlark(inputMap)
	assert.NilError(t, err, "convertToStarlark should handle map[string]interface{}")

	dict, ok := dictVal.(*starlark.Dict)
	if !ok {
		t.Fatalf("expected *starlark.Dict, got %T", dictVal)
	}

	answer, found, err := dict.Get(starlark.String("answer"))
	assert.NilError(t, err)
	assert.Assert(t, found, "answer key should be found")
	answerInt, _ := starlark.AsInt32(answer)
	assert.Equal(t, int(answerInt), 42)

	nested, found, err := dict.Get(starlark.String("nested"))
	assert.NilError(t, err)
	assert.Assert(t, found, "nested key should be found")
	nestedList, ok := nested.(*starlark.List)
	if !ok {
		t.Fatalf("expected nested value to be *starlark.List, got %T", nested)
	}
	assert.Equal(t, nestedList.Len(), 2)
	assert.Equal(t, bool(nestedList.Index(0).(starlark.Bool)), true)
	assert.Equal(t, string(nestedList.Index(1).(starlark.String)), "test")

	nilVal, found, err := dict.Get(starlark.String("nilVal"))
	assert.NilError(t, err)
	assert.Assert(t, found, "nilVal key should be found")
	assert.Equal(t, nilVal, starlark.None)

	floatValFromMap, found, err := dict.Get(starlark.String("float64"))
	assert.NilError(t, err)
	assert.Assert(t, found, "float64 key should be found")
	assert.Equal(t, float64(floatValFromMap.(starlark.Float)), 3.5)

	stringVal, found, err := dict.Get(starlark.String("string"))
	assert.NilError(t, err)
	assert.Assert(t, found, "string key should be found")
	assert.Equal(t, string(stringVal.(starlark.String)), "hello")

	boolVal, found, err := dict.Get(starlark.String("bool"))
	assert.NilError(t, err)
	assert.Assert(t, found, "bool key should be found")
	assert.Equal(t, bool(boolVal.(starlark.Bool)), true)
}

func TestConvertToStarlarkErrors(t *testing.T) {
	_, err := convertToStarlark(struct{}{})
	assert.Error(t, err, "unsupported type struct {}")

	_, err = convertToStarlark([]interface{}{struct{}{}})
	assert.ErrorContains(t, err, "unsupported type struct {}")

	_, err = convertToStarlark(map[interface{}]interface{}{struct{}{}: "value"})
	assert.ErrorContains(t, err, "unsupported type struct {}")

	_, err = convertToStarlark(map[interface{}]interface{}{"badValue": struct{}{}})
	assert.ErrorContains(t, err, "unsupported type struct {}")

	_, err = convertToStarlark(map[string]interface{}{"badValue": struct{}{}})
	assert.ErrorContains(t, err, "unsupported type struct {}")
}

func TestConvertFromStarlarkSuccess(t *testing.T) {
	intVal, err := convertFromStarlark(starlark.MakeInt(11), reflect.TypeOf(0))
	assert.NilError(t, err)
	assert.Equal(t, intVal.(int), 11)

	floatVal, err := convertFromStarlark(starlark.Float(4.2), reflect.TypeOf(float64(0)))
	assert.NilError(t, err)
	assert.Equal(t, floatVal.(float64), 4.2)

	stringVal, err := convertFromStarlark(starlark.String("hi"), reflect.TypeOf(""))
	assert.NilError(t, err)
	assert.Equal(t, stringVal.(string), "hi")

	boolVal, err := convertFromStarlark(starlark.Bool(true), reflect.TypeOf(true))
	assert.NilError(t, err)
	assert.Equal(t, boolVal.(bool), true)

	list := starlark.NewList([]starlark.Value{starlark.MakeInt(1), starlark.MakeInt(2)})
	sliceVal, err := convertFromStarlark(list, reflect.TypeOf([]int{}))
	assert.NilError(t, err)
	assert.DeepEqual(t, sliceVal, []int{1, 2})

	dict := starlark.NewDict(0)
	assert.NilError(t, dict.SetKey(starlark.String("a"), starlark.MakeInt(1)))
	assert.NilError(t, dict.SetKey(starlark.String("b"), starlark.MakeInt(2)))

	mapVal, err := convertFromStarlark(dict, reflect.TypeOf(map[string]int{}))
	assert.NilError(t, err)
	assert.DeepEqual(t, mapVal, map[string]int{"a": 1, "b": 2})
}

func TestConvertFromStarlarkErrors(t *testing.T) {
	_, err := convertFromStarlark(starlark.String("bad"), reflect.TypeOf(0))
	assert.ErrorContains(t, err, "got string")

	_, err = convertFromStarlark(starlark.String("bad"), reflect.TypeOf(float64(0)))
	assert.Error(t, err, `failed to convert "bad" (type string) to float`)

	_, err = convertFromStarlark(starlark.MakeInt(1), reflect.TypeOf(""))
	assert.Error(t, err, "failed to convert 1 (type string) to string")

	_, err = convertFromStarlark(starlark.String("oops"), reflect.TypeOf(true))
	assert.Error(t, err, `failed to convert "oops" (type string) to bool`)

	list := starlark.NewList([]starlark.Value{starlark.String("not an int")})
	_, err = convertFromStarlark(list, reflect.TypeOf([]int{}))
	assert.ErrorContains(t, err, "got string")

	dict := starlark.NewDict(0)
	assert.NilError(t, dict.SetKey(starlark.String("key"), starlark.String("value")))

	_, err = convertFromStarlark(dict, reflect.TypeOf(map[string]int{}))
	assert.ErrorContains(t, err, "got string")
}
