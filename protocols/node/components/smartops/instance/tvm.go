package instance

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taubyte/go-interfaces/vm"
	librarySpec "github.com/taubyte/go-specs/library"
	smartOpSpec "github.com/taubyte/go-specs/smartops"
)

// Instantiate method returns a runtime, sdk plugin interface, and error.
func (i *instance) instantiate() (runtime vm.Runtime, sdkPlugin, smartOpPlugin interface{}, err error) {
	rt := make(chan rtResponse)
	i.rtRequest <- rt

	response := <-rt
	if response.runtime == nil || response.sdkPlugin == nil || response.smartOpPlugin == nil {
		return nil, nil, nil, fmt.Errorf("runtime or plugins nil")
	}

	return response.runtime, response.sdkPlugin, response.smartOpPlugin, nil
}

// Call takes namespace and id, then calls the moduled function. Returns an error.
func (f *instance) Call(runtime vm.Runtime, id interface{}) (uint32, error) {
	config := f.context.Config

	moduleName, err := f.moduleName()
	if err != nil {
		return 0, fmt.Errorf("getting module for smartOp `%s` failed with: %s", config.Name, err)
	}

	module, err := runtime.Module(moduleName)
	if err != nil {
		return 0, fmt.Errorf("creating module instance for smartOp `%s` failed with: %s", config.Name, err)
	}

	fx, err := module.Function(config.Call)
	if err != nil {
		return 0, fmt.Errorf("calling function `%s` for smartOp `%s` failed with: %s", config.Call, config.Name, err.Error())
	}

	ctx, ctxC := context.WithTimeout(f.ctx, time.Duration(config.Timeout))
	defer ctxC()

	ret := fx.Call(ctx, id)
	if f.srv.Verbose() {
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
		return 0, fmt.Errorf("calling smartOp for event %d failed with %v", id, ret.Error())
	}

	var returnVal uint32
	err = ret.Reflect(&returnVal)
	if err != nil {
		return 0, fmt.Errorf("calling smartOp for event %d failed with %v", id, err)
	}

	return returnVal, nil
}

func (f *instance) moduleName() (string, error) {
	source := f.context.Config.Source
	if source == "." || source == "" {
		return smartOpSpec.ModuleName(f.context.Config.Name), nil
	} else if strings.HasPrefix(source, librarySpec.PathVariable.String()) {
		libName := strings.TrimPrefix(source, librarySpec.PathVariable.String()+"/")

		_library, err := f.srv.Tns().Fetch(librarySpec.Tns().NameIndex(libName))
		if err != nil {
			return "", fmt.Errorf("fetching library name `%s` failed with: %s", libName, err)
		}

		library, ok := _library.Interface().(string)
		if !ok {
			return "", fmt.Errorf("expected string for library `%s` interface `%v`, got `%T`", libName, _library.Interface(), _library.Interface())
		}

		return librarySpec.ModuleName(library), nil
	} else {
		return source, nil
	}
}
