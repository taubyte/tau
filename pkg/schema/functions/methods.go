package functions

import (
	"fmt"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/common"
)

func (f *function) WrapError(format string, i ...any) error {
	return fmt.Errorf("on function `"+f.name+"`; "+format, i...)
}

func (f *function) Root() *seer.Query {
	return f.Resource.Root()
}

func (f *function) Config() *seer.Query {
	return f.Resource.Config()
}

func (f *function) Name() string {
	return f.name
}

func (f *function) AppName() string {
	return f.application
}

func (*function) Directory() string {
	return common.FunctionFolder
}

func (f *function) SetName(name string) {
	f.name = name
}
