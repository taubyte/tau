package runtime

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/taubyte/tau/core/vm"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
)

func (f *Function) moduleName() (string, error) {
	source := f.config.Source
	switch source {
	case ".", "":
		return functionSpec.ModuleName(f.config.Name), nil
	default:
		if strings.HasPrefix(source, librarySpec.PathVariable.String()) {
			libId := strings.TrimPrefix(source, librarySpec.PathVariable.String()+"/")
			_library, err := f.serviceable.Service().Tns().Fetch(librarySpec.Tns().NameIndex(libId))
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

func (*Function) printRuntimeStack(runtime vm.Runtime, err error) {
	if runtime != nil {
		fmt.Println("\n\nERROR: ")
		io.Copy(os.Stdout, runtime.Stderr())
		fmt.Println("\n\nOUT: ")
		io.Copy(os.Stdout, runtime.Stdout())
	}
	if err != nil {
		fmt.Printf("\n\nExtra out:\nRET:%s\n", err.Error())
	}
}
