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
func (v *vm) Module(mod Module) {
	v.builtins[mod.Name()] = starlark.StringDict{
		mod.Name(): starlarkstruct.FromStringDict(starlarkstruct.Default, registerMethods(mod)),
	}
}

func (v *vm) Modules(mods ...Module) {
	for _, mod := range mods {
		v.builtins[mod.Name()] = starlark.StringDict{
			mod.Name(): starlarkstruct.FromStringDict(starlarkstruct.Default, registerMethods(mod)),
		}
	}
}

func registerMethods(obj interface{}) starlark.StringDict {
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
				methods[starlarkName] = makeGoFunc(method)
			} else if validateMethodSignature(method.Type()) {
				methods[starlarkName] = makeStarlarkFunc(method)
			}
		}
	}

	return methods
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

func makeStarlarkFunc(method reflect.Value) *starlark.Builtin {
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
	})
}

func makeGoFunc(method reflect.Value) *starlark.Builtin {
	return starlark.NewBuiltin(method.Type().Name(), func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		methodType := method.Type()
		if args.Len() != methodType.NumIn() {
			return nil, fmt.Errorf("expected %d arguments, got %d", methodType.NumIn(), args.Len())
		}

		in := make([]reflect.Value, args.Len())
		for i := 0; i < args.Len(); i++ {
			arg := args.Index(i)
			in[i] = reflect.ValueOf(convertFromStarlark(arg, methodType.In(i)))
		}

		retValues := method.Call(in)
		starlarkRet := make(starlark.Tuple, 0, len(retValues))

		for _, ret := range retValues {
			starlarkRet = append(starlarkRet, convertToStarlark(ret.Interface()))
		}

		if len(starlarkRet) == 0 {
			return starlark.None, nil
		} else if len(starlarkRet) == 1 {
			return starlarkRet[0], nil
		}

		return starlarkRet, nil
	})
}
