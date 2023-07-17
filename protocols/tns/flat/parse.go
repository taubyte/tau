package flat

import (
	"fmt"
	"reflect"
)

func parseInterface(path []string, data interface{}) (Items, error) {
	dvalue := reflect.ValueOf(data)
	switch dvalue.Kind() {
	case reflect.Ptr:
		return parseInterface(path, dvalue.Elem().Interface())
	case reflect.Map:
		return parseMap(path, dvalue)
	default: // consider it's a value and try to cbor it!
		return parseValue(path, dvalue)

	}
}

func parseMap(path []string, dvalue reflect.Value) (Items, error) {
	if dvalue.Kind() != reflect.Map {
		return nil, fmt.Errorf("Failed parsing a map of type %s", dvalue.Type().String())
	}

	_items := make(Items, 0)
	for _, kval := range dvalue.MapKeys() {
		if kval.Kind() == reflect.Interface {
			kval = kval.Elem()
		}
		if kval.Kind() != reflect.String {
			return nil, fmt.Errorf("Failed parsing a map: keys of type %s", kval.Type().String())
		}
		subval := dvalue.MapIndex(kval)
		_path := make([]string, len(path))
		copy(_path, path)
		_path = append(_path, kval.String())
		__items, err := parseInterface(_path, subval.Interface())
		if err != nil {
			return nil, err
		}
		_items = append(_items, __items...)
	}
	return _items, nil
}

func parseValue(path []string, dvalue reflect.Value) (Items, error) {
	var data interface{}
	if dvalue.IsValid() == true {
		data = dvalue.Interface()
	}
	return Items{
		Item{
			Path: path,
			Data: data,
		},
	}, nil
}
