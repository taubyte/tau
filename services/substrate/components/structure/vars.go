package structure

import (
	"net/http"
	"reflect"
	"testing"

	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/p2p/streams/command"
)

type httpEvent struct {
	W http.ResponseWriter
	R *http.Request
}

var (
	AttachedTestFunctions     = make(map[string]int)
	CalledTestFunctionsPubsub = make([]pubsubIface.Message, 0)
	CalledTestFunctionsP2P    = make([]command.Body, 0)
	CalledTestFunctionsHttp   = make([]httpEvent, 0)
)

func RefreshTestVariables() {
	AttachedTestFunctions = make(map[string]int)
	CalledTestFunctionsPubsub = make([]pubsubIface.Message, 0)
	CalledTestFunctionsP2P = make([]command.Body, 0)
	CalledTestFunctionsHttp = make([]httpEvent, 0)
}

func CheckAttached(t *testing.T, expected map[string]int) bool {
	if !reflect.DeepEqual(expected, AttachedTestFunctions) {
		t.Errorf("Got attached: %#v expected %#v", AttachedTestFunctions, expected)
		return false
	}

	return true
}
