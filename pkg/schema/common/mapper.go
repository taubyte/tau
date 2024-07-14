package common

import (
	"fmt"
	"reflect"
)

func (m Mapper) Run(obj interface{}) (err error) {
	rValue := reflect.ValueOf(obj)

	switch rValue.Kind() {
	case reflect.Pointer:
		if rValue.IsNil() {
			return fmt.Errorf("nil pointer")
		}

		rValue = rValue.Elem()
	case reflect.Struct:
	default:
		return fmt.Errorf("invalid type: %T, expected struct or ptr to struct", obj)
	}

	if rValue.Kind() != reflect.Struct {
		return fmt.Errorf("%s is not a struct", rValue.Type().Name())
	}

	for _, mItem := range m {
		rField := rValue.FieldByName(mItem.Field)

		// only set if not empty
		if mItem.IfNotEmpty {
			if !rField.IsValid() || !rField.IsZero() {
				err = mItem.Callback()
			}
		} else {
			err = mItem.Callback()
		}

		if err != nil {
			return
		}
	}

	return
}
