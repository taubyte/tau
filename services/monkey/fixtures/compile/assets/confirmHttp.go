package lib

import (
	"github.com/taubyte/go-sdk-smartops/resource"
	httpEvent "github.com/taubyte/go-sdk/http/event"
)

//lint:ignore U1000 wasm export
//export confirmHttp
func confirmHttp(r resource.Resource) uint32 {
	function, err := r.Function().Http()
	if err != nil {
		return 1
	}

	name, err := function.Name()
	if err != nil {
		return 1
	}

	// Redirect if the name of the function is not "someFunc"
	if name != "someFunc" {
		e, err := r.Event()
		if err != nil {
			return 1
		}

		h, err := e.HTTP()
		if err != nil {
			return 1
		}

		_h := interface{}(h).(httpEvent.Event)
		_h.Redirect("http://www.testingmcafeesites.com/testcat_cv.html").Temporary()
	}

	return 0
}
