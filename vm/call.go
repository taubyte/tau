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

// Call takes instance and id, then calls the moduled function. Returns an error.
func (w *WasmModule) Call(runtime vm.Runtime, id interface{}) error {
	moduleName, err := w.moduleName()
	if err != nil {
		return fmt.Errorf("getting module name for resource `%s` failed with: %w", w.serviceable.Id(), err)
	}

	module, err := runtime.Module(moduleName)
	if err != nil {
		return fmt.Errorf("creating module instance failed with: %w", err)
	}

	fx, err := module.Function(w.structure.Call)
	if err != nil {
		return fmt.Errorf("getting wasm function instance failed with: %w", err)
	}

	ctx, ctxC := context.WithTimeout(w.ctx, time.Duration(time.Nanosecond*time.Duration(w.structure.Timeout)))
	defer ctxC()

	ret := fx.Call(ctx, id)
	if w.serviceable.Service().Verbose() {
		defer w.printRuntimeStack(runtime, ret)
	}
	if ret.Error() != nil {
		return fmt.Errorf("calling function for event %d failed with: %s", id, ret.Error())
	}

	return nil
}

func (w *WasmModule) moduleName() (string, error) {
	source := w.structure.Source
	switch source {
	case ".", "":
		return functionSpec.ModuleName(w.structure.Name), nil
	default:
		if strings.HasPrefix(source, librarySpec.PathVariable.String()) {
			libId := strings.TrimPrefix(source, librarySpec.PathVariable.String()+"/")
			_library, err := w.serviceable.Service().Tns().Fetch(librarySpec.Tns().NameIndex(libId))
			if err != nil {
				return "", fmt.Errorf("fetching library name for resource: `%s` failed with: %w", libId, err)
			}

			library, ok := _library.Interface().(string)
			if !ok {
				return "", fmt.Errorf("got tns object for library index %#v, expected string value ", _library.Interface())
			}

			return librarySpec.ModuleName(library), nil
		}
	}

	return source, nil
}

func (w *WasmModule) printRuntimeStack(runtime vm.Runtime, ret vm.Return) {
	if runtime != nil {
		fmt.Println("\n\nERROR: ")
		io.Copy(os.Stdout, runtime.Stderr())
		fmt.Println("\n\nOUT: ")
		io.Copy(os.Stdout, runtime.Stdout())
	}
	if ret != nil {
		fmt.Printf("\n\nExtra out:\nRET:%v\n", ret.Error())
	}
}
