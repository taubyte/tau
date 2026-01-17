package starlark

import (
	"fmt"
	"reflect"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// Builtin registers methods from a given struct that have names starting with 'E_'
// will override an existing module with the same name
// E_ followed by a capital letter export a function with native go types
func (v *vm) Module(mod Module) error {
	v.lock.Lock()
	defer v.lock.Unlock()

	if _, exists := v.builtins[mod.Name()]; exists {
		return nil
	}

	dict, err := registerMethods(mod)
	if err != nil {
		return fmt.Errorf("failed to add module `%s` with %w", mod.Name(), err)
	}

	v.builtins[mod.Name()] = starlark.StringDict{
		mod.Name(): starlarkstruct.FromStringDict(starlarkstruct.Default, dict),
	}

	return nil
}

func (v *vm) Modules(mods ...Module) error {
	v.lock.Lock()
	defer v.lock.Unlock()

	for _, mod := range mods {
		if _, exists := v.builtins[mod.Name()]; exists {
			continue
		}
		if dict, err := registerMethods(mod); err != nil {
			return fmt.Errorf("adding modules failed on module `%s` with %w", mod.Name(), err)
		} else {
			v.builtins[mod.Name()] = starlark.StringDict{
				mod.Name(): starlarkstruct.FromStringDict(starlarkstruct.Default, dict),
			}
		}
	}

	return nil
}

func registerMethods(obj any) (starlark.StringDict, error) {
	val := reflect.ValueOf(obj)
	typ := val.Type()
	numMethods := typ.NumMethod()
	methods := make(starlark.StringDict, numMethods)

	for i := 0; i < numMethods; i++ {
		method := val.Method(i)
		methodName := typ.Method(i).Name
		if starlarkName, ok := strings.CutPrefix(methodName, "E_"); ok && len(starlarkName) > 1 {
			if starlarkName[0] >= 'A' && starlarkName[0] <= 'Z' {
				starlarkName = string(starlarkName[0]+('a'-'A')) + starlarkName[1:] // converts to lowercase
				if dict, err := makeGoFunc(starlarkName, method); err == nil {
					methods[starlarkName] = dict
				} else {
					return nil, err
				}
			} else if validateMethodSignature(method.Type()) {
				if dict, err := makeStarlarkFunc(starlarkName, method); err == nil {
					methods[starlarkName] = dict
				} else {
					return nil, err
				}
			} else {
				return nil, fmt.Errorf("method %s has an invalid signature", methodName)
			}
		}
	}

	return methods, nil
}

var (
	starlarkThreadType     = reflect.TypeOf((*starlark.Thread)(nil))
	starlarkBuiltinType    = reflect.TypeOf((*starlark.Builtin)(nil))
	starlarkTupleType      = reflect.TypeOf(starlark.Tuple(nil))
	starlarkTupleSliceType = reflect.TypeOf([]starlark.Tuple(nil))
	starlarkValueInterface = reflect.TypeOf((*starlark.Value)(nil)).Elem()
	errorInterface         = reflect.TypeOf((*error)(nil)).Elem()
)

func validateMethodSignature(t reflect.Type) bool {
	return t.NumIn() == 4 &&
		t.In(0).AssignableTo(starlarkThreadType) &&
		t.In(1).AssignableTo(starlarkBuiltinType) &&
		t.In(2).AssignableTo(starlarkTupleType) &&
		t.In(3).AssignableTo(starlarkTupleSliceType) &&
		t.NumOut() == 2 &&
		t.Out(0).AssignableTo(starlarkValueInterface) &&
		t.Out(1).Implements(errorInterface)
}

func makeStarlarkFunc(name string, method reflect.Value) (*starlark.Builtin, error) {
	// method signature already validated
	return starlark.NewBuiltin(name, func(thread *starlark.Thread, builtin *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
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

func makeGoFunc(name string, method reflect.Value) (*starlark.Builtin, error) {
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
			val, err := convertFromStarlark(args.Index(i), methodType.In(i))
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

	return starlark.NewBuiltin(name, wfunc), nil
}
