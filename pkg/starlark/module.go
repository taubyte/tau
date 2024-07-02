package starlark

import (
	"fmt"
	"reflect"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"golang.org/x/exp/maps"
)

// Builtin registers methods from a given struct that have names starting with 'E_'
// will override an existing module with the same name
// E_ followed by a capital letter export a function with native go types
func (v *vm) Module(mod Module) error {
	v.lock.Lock()
	defer v.lock.Unlock()

	if dict, err := registerMethods(mod); err == nil {
		v.builtins[mod.Name()] = starlark.StringDict{
			mod.Name(): starlarkstruct.FromStringDict(starlarkstruct.Default, dict),
		}

		return nil
	} else {
		return fmt.Errorf("failed to add module `%s` with %w", mod.Name(), err)
	}
}

func (v *vm) Modules(mods ...Module) error {
	v.lock.Lock()
	defer v.lock.Unlock()

	builtins := make(map[string]starlark.StringDict)
	for _, mod := range mods {
		if dict, err := registerMethods(mod); err != nil {
			return fmt.Errorf("adding modules failed on module `%s` with %w", mod.Name(), err)
		} else {
			builtins[mod.Name()] = starlark.StringDict{
				mod.Name(): starlarkstruct.FromStringDict(starlarkstruct.Default, dict),
			}
		}
	}

	maps.Copy(v.builtins, builtins)
	return nil
}

func registerMethods(obj interface{}) (starlark.StringDict, error) {
	methods := make(starlark.StringDict)
	val := reflect.ValueOf(obj)
	typ := val.Type()

	for i := 0; i < typ.NumMethod(); i++ {
		method := val.Method(i)
		methodName := typ.Method(i).Name

		if strings.HasPrefix(methodName, "E_") && len(methodName) > 2 {
			starlarkName := strings.TrimPrefix(methodName, "E_")

			if strings.ToUpper(string(methodName[2])) == string(methodName[2]) {
				starlarkName = strings.ToLower(string(starlarkName[0])) + starlarkName[1:]
				if dict, err := makeGoFunc(method); err == nil {
					methods[starlarkName] = dict
				} else {
					return nil, err
				}
			} else if validateMethodSignature(method.Type()) {
				if dict, err := makeStarlarkFunc(method); err == nil {
					methods[starlarkName] = dict
				} else {
					return nil, err
				}
			}
		}
	}

	return methods, nil
}

func validateMethodSignature(t reflect.Type) bool {
	return t.NumIn() == 4 &&
		t.In(0).AssignableTo(reflect.TypeOf((*starlark.Thread)(nil))) &&
		t.In(1).AssignableTo(reflect.TypeOf((*starlark.Builtin)(nil))) &&
		t.In(2).AssignableTo(reflect.TypeOf(starlark.Tuple(nil))) &&
		t.In(3).AssignableTo(reflect.TypeOf([]starlark.Tuple(nil))) &&
		t.NumOut() == 2 &&
		t.Out(0).AssignableTo(reflect.TypeOf((*starlark.Value)(nil)).Elem()) &&
		t.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem())
}

func makeStarlarkFunc(method reflect.Value) (*starlark.Builtin, error) {
	return starlark.NewBuiltin(method.Type().Name(), func(thread *starlark.Thread, builtin *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		retValues := method.Call([]reflect.Value{
			reflect.ValueOf(thread),
			reflect.ValueOf(builtin),
			reflect.ValueOf(args),
			reflect.ValueOf(kwargs),
		})
		result := retValues[0].Interface()
		err := retValues[1].Interface()
		if err != nil {
			return nil, err.(error)
		}
		return result.(starlark.Value), nil
	}), nil
}

var errorInterface = reflect.TypeOf((*error)(nil)).Elem()

// Check if the type is supported
func isSupportedType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Int, reflect.Float64, reflect.String, reflect.Bool, reflect.Interface:
		return true
	case reflect.Slice:
		return isSupportedType(t.Elem())
	case reflect.Map:
		return isSupportedType(t.Key()) && isSupportedType(t.Elem())
	default:
		return false
	}
}

func makeGoFunc(method reflect.Value) (*starlark.Builtin, error) {
	// Check for unsupported argument types
	methodType := method.Type()
	for i := 0; i < methodType.NumIn(); i++ {
		argType := methodType.In(i)
		if !isSupportedType(argType) {
			return nil, fmt.Errorf("unsupported argument type: %s", argType)
		}
	}

	wfunc := func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		methodType := method.Type()
		if args.Len() != methodType.NumIn() {
			return nil, fmt.Errorf("expected %d arguments, got %d", methodType.NumIn(), args.Len())
		}

		in := make([]reflect.Value, args.Len())
		for i := 0; i < args.Len(); i++ {
			arg := args.Index(i)
			val, err := convertFromStarlark(arg, methodType.In(i))
			if err != nil {
				return nil, err
			}

			in[i] = reflect.ValueOf(val)
		}

		retValues := method.Call(in)

		if len(retValues) > 0 && retValues[len(retValues)-1].Type().Implements(errorInterface) {
			if err, _ := retValues[len(retValues)-1].Interface().(error); err != nil {
				return nil, err
			} else {
				retValues = retValues[:len(retValues)-1]
			}
		}

		starlarkRet := make(starlark.Tuple, 0, len(retValues))

		for _, ret := range retValues {
			val, err := convertToStarlark(ret.Interface())
			if err != nil {
				return nil, err
			}
			starlarkRet = append(starlarkRet, val)
		}

		if len(starlarkRet) == 0 {
			return starlark.None, nil
		} else if len(starlarkRet) == 1 {
			return starlarkRet[0], nil
		}

		return starlarkRet, nil
	}

	return starlark.NewBuiltin(method.Type().Name(), wfunc), nil
}
