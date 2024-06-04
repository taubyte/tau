package satellite

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
)

type signatureParseType bool

const (
	in  signatureParseType = true
	out signatureParseType = false
)

func parseSignatureValues(fx reflect.Type, parseType signatureParseType) ([]proto.Type, error) {
	var handler func(i int) reflect.Type
	var size int

	if parseType == in {
		handler = fx.In
		size = fx.NumIn()
	} else {
		handler = fx.Out
		size = fx.NumOut()
	}

	types := make([]proto.Type, 0, size)
	for i := 0; i < size; i++ {
		if parseType == in {
			if (i == 0 && handler(i).Implements(vm.ContextType)) || (i == 1 && handler(i).Implements(moduleType)) {
				continue
			}
		}

		switch handler(i).Kind() {
		case reflect.Int32, reflect.Uint32:
			types = append(types, proto.Type_i32)
		case reflect.Int64, reflect.Uint64:
			types = append(types, proto.Type_i64)
		case reflect.Float32:
			types = append(types, proto.Type_f32)
		case reflect.Float64:
			types = append(types, proto.Type_f64)
		default:
			return nil, errors.New("invalid value type")
		}
	}

	return types, nil
}

func serverError(format string, args ...interface{}) error {
	return fmt.Errorf("[server] "+format, args...)
}
