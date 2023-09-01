package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/taubyte/go-interfaces/vm"
)

// Call takes instance and id, then calls the moduled function. Returns an error.
func (d *DFunc) Call(runtime vm.Runtime, id interface{}) error {
	moduleName, err := d.moduleName()
	if err != nil {
		return fmt.Errorf("getting module name for resource `%s` failed with: %w", d.serviceable.Id(), err)
	}

	module, err := runtime.Module(moduleName)
	if err != nil {
		return fmt.Errorf("creating module instance failed with: %w", err)
	}

	fx, err := module.Function(d.structure.Call)
	if err != nil {
		return fmt.Errorf("getting wasm function instance failed with: %w", err)
	}

	ctx, ctxC := context.WithTimeout(d.ctx, time.Duration(time.Nanosecond*time.Duration(d.structure.Timeout)))
	defer ctxC()

	ret := fx.Call(ctx, id)
	if d.serviceable.Service().Verbose() {
		defer d.printRuntimeStack(runtime, ret)
	}
	if ret.Error() != nil {
		return fmt.Errorf("calling function for event %d failed with: %s", id, ret.Error())
	}

	return nil
}
