package instance

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate"
	smartOpPlugins "github.com/taubyte/vm-core-plugins/smartops"
)

func (i *instance) Run(caller substrate.SmartOpEventCaller) (uint32, error) {
	runtime, _, smartOpPlugin, err := i.instantiate()
	if err != nil {
		return 0, fmt.Errorf("instantiating runtime failed with: %v", err)
	}
	defer runtime.Close()

	smartOpSdk, ok := smartOpPlugin.(smartOpPlugins.Instance)
	if !ok {
		return 0, fmt.Errorf("smartops Plugin is not a plugin instance `%T`", smartOpPlugin)
	}

	resource := smartOpSdk.CreateSmartOp(caller)

	// TODO: FIX ME

	// sdk, ok := sdkPlugin.(sdkPlugins.Instance)
	// if !ok {
	// 	return 0, fmt.Errorf("smartops Plugin is not a plugin instance `%T`", smartOpPlugin)
	// }

	// _event := caller.Event()
	// if _event != nil {
	// 	sdkEvent, ok := caller.Event().(*event.Event)
	// 	if !ok {
	// 		return 0, fmt.Errorf("event is not a plugin event `%T`", caller.Event())
	// 	}
	// 	sdk.AttachEvent(sdkEvent)
	// }
	return i.Call(runtime, resource.Id)
}
