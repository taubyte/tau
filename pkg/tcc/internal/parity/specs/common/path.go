package common

import "github.com/taubyte/tau/utils/path"

func NewTnsPath(value []string) *TnsPath {
	return &TnsPath{value: value}
}

func (pv PathVariable) String() string {
	return string(pv)
}

func (_path *TnsPath) String() string {
	if len(_path.strValue) == 0 {
		_path.strValue = path.Join(_path.value)
	}

	return _path.strValue
}

func (_path *TnsPath) Slice() []string {
	return _path.value
}
