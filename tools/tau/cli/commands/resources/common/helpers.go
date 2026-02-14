package resources

import "reflect"

func PanicIfMissingValue(h any) {
	if h == nil || reflect.ValueOf(h).IsNil() {
		panic("PanicIfMissingValue: handler is nil")
	}
	for i := 0; i < reflect.TypeOf(h).Elem().NumField(); i++ {
		field := reflect.TypeOf(h).Elem().Field(i)
		if field.Type.Kind() == reflect.Func {
			if reflect.ValueOf(h).Elem().Field(i).IsNil() {
				panic(field.Name + " is nil")
			}
		}
		if field.Type.Kind() == reflect.String {
			if reflect.ValueOf(h).Elem().Field(i).String() == "" {
				panic(field.Name + " is empty")
			}
		}
	}
}
