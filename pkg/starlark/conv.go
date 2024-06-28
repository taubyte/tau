package starlark

import (
	"fmt"
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

func convertFromStarlark(val starlark.Value, targetType reflect.Type) (interface{}, error) {
	switch targetType.Kind() {
	case reflect.Int:
		intVal, err := starlark.AsInt32(val)
		return int(intVal), err
	case reflect.Float64:
		floatVal, ok := starlark.AsFloat(val)
		if !ok {
			return nil, fmt.Errorf("failed to convert %s (type %T) to float", val.String(), val.Type())
		}
		return floatVal, nil
	case reflect.String:
		sval, ok := val.(starlark.String)
		if !ok {
			return nil, fmt.Errorf("failed to convert %s (type %T) to string", val.String(), val.Type())
		}
		return sval.GoString(), nil
	case reflect.Bool:
		bval, ok := val.(starlark.Bool)
		if !ok {
			return nil, fmt.Errorf("failed to convert %s (type %T) to bool", val.String(), val.Type())
		}
		return bool(bval), nil
	case reflect.Slice:
		list := val.(*starlark.List)
		slice := reflect.MakeSlice(targetType, list.Len(), list.Len())
		for i := 0; i < list.Len(); i++ {
			val, err := convertFromStarlark(list.Index(i), targetType.Elem())
			if err != nil {
				return nil, err
			}
			slice.Index(i).Set(reflect.ValueOf(val))
		}
		return slice.Interface(), nil
	case reflect.Map:
		dict := val.(*starlark.Dict)
		mapType := reflect.MapOf(targetType.Key(), targetType.Elem())
		mapVal := reflect.MakeMap(mapType)
		for _, item := range dict.Items() {
			key, err := convertFromStarlark(item[0], targetType.Key())
			if err != nil {
				return nil, err
			}
			value, err := convertFromStarlark(item[1], targetType.Elem())
			if err != nil {
				return nil, err
			}
			mapVal.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(value))
		}
		return mapVal.Interface(), nil
	default:
		return val, nil
	}
}

func convertToStarlark(val interface{}) (starlark.Value, error) {
	switch v := val.(type) {
	case int:
		return starlark.MakeInt(v), nil
	case float64:
		return starlark.Float(v), nil
	case string:
		return starlark.String(v), nil
	case bool:
		return starlark.Bool(v), nil
	case []interface{}:
		list := &starlark.List{}
		for _, item := range v {
			i, err := convertToStarlark(item)
			if err != nil {
				return nil, err
			}

			err = list.Append(i)
			if err != nil {
				return nil, err
			}
		}
		return list, nil
	case map[interface{}]interface{}:
		dict := starlark.NewDict(len(v))
		for key, value := range v {
			k, err := convertToStarlark(key)
			if err != nil {
				return nil, err
			}

			v, err := convertToStarlark(value)
			if err != nil {
				return nil, err
			}

			err = dict.SetKey(k, v)
			if err != nil {
				return nil, err
			}
		}

		return dict, nil
	case nil:
		return starlark.None, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", val)
	}
}
