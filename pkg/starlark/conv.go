package starlark

import (
	"reflect"

	"go.starlark.net/starlark"
)

func convertFromStarlarkBasedOnValue(val starlark.Value) any {
	switch v := val.(type) {
	case starlark.Int:
		intVal, _ := v.Int64()
		return intVal
	case starlark.Float:
		return float64(v)
	case starlark.String:
		return string(v)
	case starlark.Bool:
		return bool(v)
	case *starlark.List:
		result := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			result[i] = convertFromStarlarkBasedOnValue(v.Index(i))
		}
		return result
	case *starlark.Dict:
		result := make(map[any]any)
		for _, item := range v.Items() {
			key := convertFromStarlarkBasedOnValue(item[0])
			value := convertFromStarlarkBasedOnValue(item[1])
			result[key] = value
		}
		return result
	case starlark.NoneType:
		return nil
	default:
		return val
	}
}

func convertFromStarlark(val starlark.Value, targetType reflect.Type) interface{} {
	switch targetType.Kind() {
	case reflect.Int:
		intVal, _ := starlark.AsInt32(val)
		return int(intVal)
	case reflect.Float64:
		floatVal, _ := starlark.AsFloat(val)
		return floatVal
	case reflect.String:
		return string(val.(starlark.String))
	case reflect.Bool:
		return bool(val.(starlark.Bool))
	case reflect.Slice:
		list := val.(*starlark.List)
		slice := reflect.MakeSlice(targetType, list.Len(), list.Len())
		for i := 0; i < list.Len(); i++ {
			slice.Index(i).Set(reflect.ValueOf(convertFromStarlark(list.Index(i), targetType.Elem())))
		}
		return slice.Interface()
	case reflect.Map:
		dict := val.(*starlark.Dict)
		mapType := reflect.MapOf(targetType.Key(), targetType.Elem())
		mapVal := reflect.MakeMap(mapType)
		for _, item := range dict.Items() {
			key := convertFromStarlark(item[0], targetType.Key())
			value := convertFromStarlark(item[1], targetType.Elem())
			mapVal.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(value))
		}
		return mapVal.Interface()
	default:
		return val
	}
}

func convertToStarlark(val interface{}) starlark.Value {
	switch v := val.(type) {
	case int:
		return starlark.MakeInt(v)
	case float64:
		return starlark.Float(v)
	case string:
		return starlark.String(v)
	case bool:
		return starlark.Bool(v)
	case []interface{}:
		list := &starlark.List{}
		for _, item := range v {
			list.Append(convertToStarlark(item))
		}
		return list
	case map[interface{}]interface{}:
		dict := starlark.NewDict(len(v))
		for key, value := range v {
			dict.SetKey(convertToStarlark(key), convertToStarlark(value))
		}
		return dict
	case nil:
		return starlark.None
	default:
		return nil
	}
}
