package helpers

import (
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (m *methods) WriteBool(
	module common.Module,
	ptr uint32,
	value bool,
) errno.Error {
	var _value uint32
	if value {
		_value = 1
	}

	return m.WriteUint32Le(module, ptr, _value)
}

func (m *methods) ReadBool(
	module common.Module,
	val uint32,
) (bool, errno.Error) {
	switch val {
	case 0:
		return false, 0
	case 1:
		return true, 0
	default:
		return false, errno.ErrorInvalidBool
	}
}
