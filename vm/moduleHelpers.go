package vm

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/taubyte/go-interfaces/vm"
	functionSpec "github.com/taubyte/go-specs/function"
	librarySpec "github.com/taubyte/go-specs/library"
)

func (w *DFunc) moduleName() (string, error) {
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

func (d *DFunc) printRuntimeStack(runtime vm.Runtime, ret vm.Return) {
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
