package tvm

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taubyte/go-interfaces/vm"
	functionSpec "github.com/taubyte/go-specs/function"
	librarySpec "github.com/taubyte/go-specs/library"
)

func (f *FunctionInstance) moduleName() (string, error) {
	source := f.config.Source
	switch source {
	case ".", "":
		return functionSpec.ModuleName(f.config.Name), nil
	default:
		if strings.HasPrefix(source, librarySpec.PathVariable.String()) {
			libId := strings.TrimPrefix(source, librarySpec.PathVariable.String()+"/")

			_library, err := f.parent.srv.Tns().Fetch(librarySpec.Tns().NameIndex(libId))
			if err != nil {
				return "", fmt.Errorf("fetching library name for libraryId: `%s` failed with: %s", libId, err)
			}

			library, ok := _library.Interface().(string)
			if !ok {
				return "", fmt.Errorf("expected string for library `%s` interface `%v`, got `%T`", libId, _library.Interface(), _library.Interface())
			}

			return librarySpec.ModuleName(library), nil
		}

		return source, nil
	}
}

// Call takes instance and id, then calls the moduled function. Returns an error.
func (f *FunctionInstance) Call(runtime vm.Runtime, id interface{}) error {
	moduleName, err := f.moduleName()
	if err != nil {
		return fmt.Errorf("getting module name for source `%s` failed with: %s", f.config.Source, err)
	}

	module, err := runtime.Module(moduleName)
	if err != nil {
		return fmt.Errorf("creating module instance for function `%s` failed with: %s", f.config.Name, err)
	}

	fx, err := module.Function(f.config.Call)
	if err != nil {
		return fmt.Errorf("calling function `%s` for function `%s` failed with: %s", f.config.Call, f.config.Name, err.Error())
	}

	ctx, ctxC := context.WithTimeout(f.parent.srv.Context(), time.Duration(f.config.Timeout))
	defer ctxC()

	ret := fx.Call(ctx, id)
	if f.parent.Verbose() {
		defer func() {
			fmt.Println("\n\nERROR: ")
			io.Copy(os.Stdout, runtime.Stderr())
			fmt.Println("\n\nOUT: ")
			io.Copy(os.Stdout, runtime.Stdout())
			fmt.Println("\n\nExtra out: ")
			fmt.Printf("RET: %v\n", ret.Error())
		}()
	}
	if ret.Error() != nil {
		return fmt.Errorf("calling function for event %d failed with: %s", id, ret.Error())
	}

	return nil
}
